package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorReset  = "\033[0m"
)

// defaultWaitTimeout bounds every wait. An unbounded wait blocks whoever started
// it until the run ends, which can be never: a queued job with no free runner
// never starts. --timeout 0 restores the unbounded wait for a caller who wants it.
const defaultWaitTimeout = 30 * time.Minute

func printError(msg string) {
	fmt.Fprintf(os.Stderr, "%sERROR: %s%s\n", colorRed, msg, colorReset)
}

func printInfo(msg string) {
	fmt.Printf("%s%s%s\n", colorBlue, msg, colorReset)
}

func printSuccess(msg string) {
	fmt.Printf("%s%s%s\n", colorGreen, msg, colorReset)
}

func printWarn(msg string) {
	fmt.Printf("%s%s%s\n", colorYellow, msg, colorReset)
}

type Context struct {
	Commit      string
	ShortCommit string
	Branch      string
	Repo        string
	CommitURL   string
	PRURL       string
	PRNum       string
}

type RunInfo struct {
	DatabaseID int    `json:"databaseId"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
}

type Job struct {
	DatabaseID int    `json:"databaseId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Steps      []Step `json:"steps"`
}

type Step struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type RunDetail struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Jobs       []Job  `json:"jobs"`
}

type PRInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type RepoInfo struct {
	NameWithOwner string `json:"nameWithOwner"`
}

type RemoteRepoInfo struct {
	DefaultBranch string `json:"default_branch"`
}

type RemoteCommitInfo struct {
	SHA string `json:"sha"`
}

// repoFlag holds the --repo/-R flag value. When set, it overrides repo detection
// and causes gh commands to target the specified repository.
var repoFlag string

// ghCommand runs a gh CLI command, automatically injecting -R <repo> when repoFlag is set.
func ghCommand(args ...string) (string, error) {
	if repoFlag != "" {
		args = append([]string{"-R", repoFlag}, args...)
	}
	return runCommand("gh", args...)
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// findGitRepo checks if the current directory is inside a git repository.
// If not, it searches immediate subdirectories for git repos and changes
// into the directory if exactly one is found.
func findGitRepo() error {
	_, err := runCommand("git", "rev-parse", "--git-dir")
	if err == nil {
		return nil
	}

	// Not in a git repo — search immediate subdirectories
	entries, err := os.ReadDir(".")
	if err != nil {
		return fmt.Errorf("not in a git repository")
	}

	var repos []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, statErr := os.Stat(entry.Name() + "/.git"); statErr == nil {
			repos = append(repos, entry.Name())
		}
	}

	switch len(repos) {
	case 0:
		return fmt.Errorf("not in a git repository")
	case 1:
		printInfo(fmt.Sprintf("Found git repository in ./%s, using it", repos[0]))
		if err := os.Chdir(repos[0]); err != nil {
			return fmt.Errorf("could not enter repository %s: %w", repos[0], err)
		}
		return nil
	default:
		return fmt.Errorf("not in a git repository, found multiple repositories: %s\nPlease cd into one or use --repo/-R", strings.Join(repos, ", "))
	}
}

// checkPushed checks if there are unpushed commits. Returns the commit to use
// (either HEAD if all pushed, or the upstream commit if unpushed commits exist)
// and a boolean indicating if we fell back to the upstream commit.
func checkPushed() (string, bool, error) {
	unpushed, err := runCommand("git", "log", "@{u}..HEAD", "--oneline")
	if err != nil {
		// If there's no upstream, use HEAD (can't determine pushed state)
		return "HEAD", false, nil
	}
	if unpushed != "" {
		// Get the upstream commit to use instead
		upstreamCommit, err := runCommand("git", "rev-parse", "@{u}")
		if err != nil {
			return "", false, fmt.Errorf("could not get upstream commit: %w", err)
		}
		return upstreamCommit, true, nil
	}
	return "HEAD", false, nil
}

func getContext(commitRef string) (*Context, error) {
	ctx := &Context{}

	if repoFlag != "" {
		return getRemoteContext(commitRef)
	}

	commit, err := runCommand("git", "rev-parse", commitRef)
	if err != nil {
		return nil, fmt.Errorf("could not get commit: %w", err)
	}
	ctx.Commit = commit

	shortCommit, err := runCommand("git", "rev-parse", "--short", commitRef)
	if err != nil {
		return nil, fmt.Errorf("could not get short commit: %w", err)
	}
	ctx.ShortCommit = shortCommit

	branch, err := runCommand("git", "branch", "--show-current")
	if err != nil {
		return nil, fmt.Errorf("could not get branch: %w", err)
	}
	ctx.Branch = branch

	repoJSON, err := runCommand("gh", "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return nil, fmt.Errorf("could not determine GitHub repository")
	}

	var repoInfo RepoInfo
	if err := json.Unmarshal([]byte(repoJSON), &repoInfo); err != nil {
		return nil, fmt.Errorf("could not parse repo info: %w", err)
	}
	ctx.Repo = repoInfo.NameWithOwner

	ctx.CommitURL = fmt.Sprintf("https://github.com/%s/commit/%s", ctx.Repo, ctx.Commit)

	return ctx, nil
}

// getRemoteContext builds a Context by querying the remote repository specified by repoFlag.
// When commitRef is a specific SHA (not "HEAD"), it resolves that SHA directly via the API.
// Otherwise it looks up the latest commit on the default branch.
func getRemoteContext(commitRef string) (*Context, error) {
	ctx := &Context{}
	ctx.Repo = repoFlag

	if commitRef != "" && commitRef != "HEAD" {
		// Resolve the specific SHA via the API (handles partial SHAs)
		commitJSON, err := runCommand("gh", "api", fmt.Sprintf("repos/%s/commits/%s", repoFlag, commitRef))
		if err != nil {
			return nil, fmt.Errorf("could not get commit %s for %s: %w", commitRef, repoFlag, err)
		}

		var commitInfo RemoteCommitInfo
		if err := json.Unmarshal([]byte(commitJSON), &commitInfo); err != nil {
			return nil, fmt.Errorf("could not parse commit info: %w", err)
		}
		ctx.Commit = commitInfo.SHA
		if len(ctx.Commit) >= 7 {
			ctx.ShortCommit = ctx.Commit[:7]
		} else {
			ctx.ShortCommit = ctx.Commit
		}
		ctx.CommitURL = fmt.Sprintf("https://github.com/%s/commit/%s", ctx.Repo, ctx.Commit)
		return ctx, nil
	}

	// Get the default branch of the remote repo
	repoJSON, err := runCommand("gh", "api", fmt.Sprintf("repos/%s", repoFlag))
	if err != nil {
		return nil, fmt.Errorf("could not query repository %s: %w", repoFlag, err)
	}

	var remoteRepo RemoteRepoInfo
	if err := json.Unmarshal([]byte(repoJSON), &remoteRepo); err != nil {
		return nil, fmt.Errorf("could not parse repo info: %w", err)
	}
	ctx.Branch = remoteRepo.DefaultBranch

	// Get the latest commit on the default branch
	commitJSON, err := runCommand("gh", "api", fmt.Sprintf("repos/%s/commits/%s", repoFlag, ctx.Branch))
	if err != nil {
		return nil, fmt.Errorf("could not get latest commit for %s: %w", ctx.Branch, err)
	}

	var commitInfo RemoteCommitInfo
	if err := json.Unmarshal([]byte(commitJSON), &commitInfo); err != nil {
		return nil, fmt.Errorf("could not parse commit info: %w", err)
	}
	ctx.Commit = commitInfo.SHA
	if len(ctx.Commit) >= 7 {
		ctx.ShortCommit = ctx.Commit[:7]
	} else {
		ctx.ShortCommit = ctx.Commit
	}

	ctx.CommitURL = fmt.Sprintf("https://github.com/%s/commit/%s", ctx.Repo, ctx.Commit)

	return ctx, nil
}

func printContext(ctx *Context) {
	printInfo(fmt.Sprintf("Repository: %s", ctx.Repo))
	if ctx.Branch != "" {
		printInfo(fmt.Sprintf("Branch: %s", ctx.Branch))
	}
	printInfo(fmt.Sprintf("Commit: %s", ctx.ShortCommit))
	fmt.Println()
}

func getPRInfo(ctx *Context) {
	prJSON, err := ghCommand("pr", "view", "--json", "number,url")
	if err != nil {
		return
	}

	var prInfo PRInfo
	if err := json.Unmarshal([]byte(prJSON), &prInfo); err != nil {
		return
	}

	ctx.PRNum = strconv.Itoa(prInfo.Number)
	ctx.PRURL = prInfo.URL
}

// newRootCmd assembles the whole command tree.
func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "gh-wait-ci [run-id]",
		Short: "Wait for GitHub Actions CI, and read or query its logs",
		Long: "Wait for GitHub Actions CI to complete and report results.\n" +
			"If no run-id is provided, waits for ALL runs for the current commit.\n\n" +
			"The subcommands cover the rest of the Actions surface, so nothing here\n" +
			"needs `gh run`:\n" +
			"  runs         list workflow runs\n" +
			"  view         a run's jobs, steps and timings\n" +
			"  jobs         a run's jobs with their IDs\n" +
			"  log          print logs, filtered by job, step or outcome\n" +
			"  grep         search logs for a pattern\n" +
			"  annotations  the errors and warnings that never reach the logs\n" +
			"  checks       every check on a commit, check runs AND commit statuses\n" +
			"  artifacts    list and download a run's artifacts\n" +
			"  workflows    the repository's workflow definitions\n" +
			"  dispatch     start a workflow_dispatch run\n" +
			"  cancel       cancel a run\n" +
			"  rerun        re-run a run, its failed jobs, or one job",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          run,
	}

	rootCmd.Flags().BoolP("fail-fast", "", false, "Exit immediately when any job fails")
	rootCmd.PersistentFlags().StringVarP(&repoFlag, "repo", "R", "", "Target repository in [HOST/]OWNER/REPO format")
	rootCmd.Flags().StringP("sha", "s", "", "Commit SHA to watch (full or partial)")
	rootCmd.Flags().BoolP("logs", "l", false, "Stream job logs live as they run, instead of a status summary")
	rootCmd.Flags().IntP("interval", "i", 5, "Polling interval in seconds")
	rootCmd.Flags().Duration("timeout", defaultWaitTimeout, "Give up waiting after this long (0 waits with no limit)")

	watchCmd := &cobra.Command{
		Use:   "watch [run-id]",
		Short: "Wait for CI to finish (the same thing the bare command does)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  run,
	}
	watchCmd.Flags().BoolP("fail-fast", "", false, "Exit immediately when any job fails")
	watchCmd.Flags().StringP("sha", "s", "", "Commit SHA to watch (full or partial)")
	watchCmd.Flags().BoolP("logs", "l", false, "Stream job logs live as they run, instead of a status summary")
	watchCmd.Flags().IntP("interval", "i", 5, "Polling interval in seconds")
	watchCmd.Flags().Duration("timeout", defaultWaitTimeout, "Give up waiting after this long (0 waits with no limit)")

	rootCmd.AddCommand(
		watchCmd,
		newRunsCmd(),
		newViewCmd(),
		newJobsCmd(),
		newLogCmd(),
		newGrepCmd(),
		newAnnotationsCmd(),
		newChecksCmd(),
		newArtifactsCmd(),
		newWorkflowsCmd(),
		newDispatchCmd(),
		newCancelCmd(),
		newRerunCmd(),
	)
	return rootCmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// A silent error carries an exit code and nothing to say: `grep` uses it
		// to exit non-zero on no match, the way grep itself does.
		var silent *silentError
		if !errors.As(err, &silent) {
			printError(err.Error())
		}
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	failFast, _ := cmd.Flags().GetBool("fail-fast")
	shaFlag, _ := cmd.Flags().GetString("sha")
	logsFlag, _ := cmd.Flags().GetBool("logs")
	intervalSec, _ := cmd.Flags().GetInt("interval")
	interval := time.Duration(intervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	// When --repo is not set, we need to be in a git repository.
	// If we're not in one, try to find one in a subdirectory.
	if repoFlag == "" {
		if err := findGitRepo(); err != nil {
			return err
		}
	}

	runID := ""
	if len(args) > 0 {
		runID = args[0]
	}

	// Determine which commit to use
	commitRef := "HEAD"
	if shaFlag != "" {
		commitRef = shaFlag
	} else if runID == "" && repoFlag == "" {
		// Only check for unpushed commits when auto-detecting runs from local repo
		ref, usedUpstream, err := checkPushed()
		if err != nil {
			return err
		}
		if usedUpstream {
			printWarn("Warning: You have unpushed commits. Watching the latest pushed commit instead.")
			fmt.Println()
		}
		commitRef = ref
	}

	ctx, err := getContext(commitRef)
	if err != nil {
		return err
	}

	printContext(ctx)
	getPRInfo(ctx)

	runIDs, err := findRuns(ctx, runID)
	if err != nil {
		return err
	}

	var hasFailure bool
	if logsFlag {
		hasFailure, err = streamLogs(runIDs, ctx, failFast, interval, deadline)
	} else {
		hasFailure, err = waitForRuns(runIDs, failFast, interval, deadline)
	}
	if err != nil {
		return err
	}

	if failFast && hasFailure {
		showResults(runIDs, ctx)
		return fmt.Errorf("CI failed")
	}

	if !showResults(runIDs, ctx) {
		return fmt.Errorf("CI failed")
	}

	return nil
}
