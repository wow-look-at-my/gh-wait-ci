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

// repoFlag holds the OWNER/REPO the --repo/-R flag named. When set, it overrides
// repo detection and causes gh commands to target the specified repository.
var repoFlag string

// repoHost holds the HOST from a --repo written as HOST/OWNER/REPO, and is empty
// otherwise.
//
// `gh api` takes a host through --hostname rather than in the path, so the two
// are kept apart here. Leaving the host on the front of repoFlag builds
// `repos/HOST/OWNER/REPO`, which is a path of the wrong shape and answers 404.
var repoHost string

// splitRepoTarget separates the optional leading host from OWNER/REPO.
func splitRepoTarget(value string) (host, repo string) {
	parts := strings.Split(value, "/")
	if len(parts) == 3 {
		return parts[0], parts[1] + "/" + parts[2]
	}
	return "", value
}

// repoTarget rebuilds what the user typed, for the `gh` subcommands that take a
// host on -R themselves.
func repoTarget() string {
	if repoHost == "" {
		return repoFlag
	}
	return repoHost + "/" + repoFlag
}

// ghCommand runs a gh CLI command, automatically injecting -R <repo> when repoFlag is set.
func ghCommand(args ...string) (string, error) {
	if repoFlag != "" {
		args = append([]string{"-R", repoTarget()}, args...)
	}
	return runCommand("gh", args...)
}

// ghEnv marks a `gh` call as this tool's own. An agent environment can block the
// raw Actions surface of `gh` to force every read through this tool; the block
// must not then break the tool, which reaches that surface by design.
func ghEnv() []string {
	return append(os.Environ(), "GH_WAIT_CI=1")
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if name == "gh" {
		cmd.Env = ghEnv()
	}
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

<<<<<<< HEAD
func getRepoFromRemote() (string, error) {
	// Try to parse origin remote URL directly (preferred - avoids gh repo view picking wrong remote)
	remoteURL, err := runCommand("git", "remote", "get-url", "origin")
	if err == nil {
		// Handle SSH format: git@github.com:owner/repo.git
		if strings.HasPrefix(remoteURL, "git@github.com:") {
			repo := strings.TrimPrefix(remoteURL, "git@github.com:")
			repo = strings.TrimSuffix(repo, ".git")
			return repo, nil
		}

		// Handle HTTPS format: https://github.com/owner/repo.git
		if strings.HasPrefix(remoteURL, "https://github.com/") {
			repo := strings.TrimPrefix(remoteURL, "https://github.com/")
			repo = strings.TrimSuffix(repo, ".git")
			return repo, nil
		}
	}

	// Fall back to gh repo view
	repoJSON, err := runCommand("gh", "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return "", fmt.Errorf("could not determine repository")
	}

	var repoInfo RepoInfo
	if err := json.Unmarshal([]byte(repoJSON), &repoInfo); err != nil {
		return "", fmt.Errorf("could not parse repo info: %w", err)
	}
	return repoInfo.NameWithOwner, nil
}

func checkPushed() error {
=======
// checkPushed returns the commit to watch, and whether it had to fall back off
// HEAD. A run only exists for a commit the remote has, so an unpushed HEAD has
// none.
//
// The reference point is the branch's OWN remote ref, not @{u}. @{u} routinely
// names a DIFFERENT branch -- `git checkout -B mine origin/master` leaves it on
// master -- and reading that as "what was pushed" watches another branch's tip
// and reports ITS result as this branch's, which is a wrong answer that looks
// exactly like a right one.
func checkPushed() (string, bool, error) {
	if branch, err := runCommand("git", "branch", "--show-current"); err == nil && branch != "" {
		ref := "refs/remotes/origin/" + branch
		if commit, err := runCommand("git", "rev-parse", "--verify", ref); err == nil && commit != "" {
			unpushed, err := runCommand("git", "log", ref+"..HEAD", "--oneline")
			if err == nil && unpushed == "" {
				return "HEAD", false, nil
			}
			return commit, true, nil
		}
	}

>>>>>>> origin/master
	unpushed, err := runCommand("git", "log", "@{u}..HEAD", "--oneline")
	if err != nil {
		// No upstream at all: use HEAD, because the pushed state is unknowable.
		return "HEAD", false, nil
	}
	if unpushed != "" {
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

	repo, err := getRepoFromRemote()
	if err != nil {
		return nil, fmt.Errorf("could not determine GitHub repository: %w", err)
	}
	ctx.Repo = repo

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
		var commitInfo RemoteCommitInfo
		if err := ghAPIJSON(fmt.Sprintf("repos/%s/commits/%s", repoFlag, commitRef), &commitInfo); err != nil {
			return nil, fmt.Errorf("could not get commit %s for %s: %w", commitRef, repoTarget(), err)
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
	var remoteRepo RemoteRepoInfo
	if err := ghAPIJSON(fmt.Sprintf("repos/%s", repoFlag), &remoteRepo); err != nil {
		return nil, fmt.Errorf("could not query repository %s: %w", repoTarget(), err)
	}
	ctx.Branch = remoteRepo.DefaultBranch

	// Get the latest commit on the default branch
	var commitInfo RemoteCommitInfo
	if err := ghAPIJSON(fmt.Sprintf("repos/%s/commits/%s", repoFlag, ctx.Branch), &commitInfo); err != nil {
		return nil, fmt.Errorf("could not get latest commit for %s: %w", ctx.Branch, err)
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
	// Splits once, before any subcommand runs, so every repos/OWNER/REPO path and every -R agree on what was asked for.
	rootCmd.PersistentPreRun = func(*cobra.Command, []string) {
		repoHost, repoFlag = splitRepoTarget(repoFlag)
	}
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

<<<<<<< HEAD
	runIDs := make([]int, len(runs))
	printInfo(fmt.Sprintf("Found %d workflow run(s):", len(runs)))
	for i, run := range runs {
		runIDs[i] = run.DatabaseID
		fmt.Printf("  %d %s\n", run.DatabaseID, run.Name)
	}
	fmt.Println()

	return runIDs, nil
}

func getRunDetail(runID int) (*RunDetail, error) {
	runJSON, err := runCommand("gh", "run", "view", strconv.Itoa(runID),
		"--json", "status,conclusion,name,jobs,url")
	if err != nil {
		return nil, err
	}

	var detail RunDetail
	if err := json.Unmarshal([]byte(runJSON), &detail); err != nil {
		return nil, err
	}

	return &detail, nil
}

// waitForRuns waits for all runs to complete. If failFast is true, returns immediately
// when any job fails. Returns (hasFailure, error).
func waitForRuns(runIDs []int, failFast bool) (bool, error) {
	printInfo("Waiting for all runs to complete...")
	fmt.Println()

	lastState := ""
	firstPrint := true
	hasFailure := false

	for {
		allDone := true
		totalJobs := 0
		completedJobs := 0
		currentState := ""
		var output strings.Builder

		for _, runID := range runIDs {
			detail, err := getRunDetail(runID)
			if err != nil {
				continue
			}

			for _, job := range detail.Jobs {
				totalJobs++
				currentState += fmt.Sprintf("%d:%s:%s:%s|", runID, job.Name, job.Status, job.Conclusion)

				var line string
				if job.Status == "completed" {
					completedJobs++
					switch job.Conclusion {
					case "success":
						line = fmt.Sprintf("  ✅ %s / %s\n", detail.Name, job.Name)
					case "skipped":
						line = fmt.Sprintf("  ⏭️  %s / %s (skipped)\n", detail.Name, job.Name)
					default:
						line = fmt.Sprintf("  ❌ %s / %s (%s)\n", detail.Name, job.Name, job.Conclusion)
						hasFailure = true
					}
				} else if job.Status == "in_progress" {
					line = fmt.Sprintf("  🔄 %s / %s\n", detail.Name, job.Name)
				} else if job.Status == "queued" || job.Status == "waiting" {
					line = fmt.Sprintf("  ⏳ %s / %s\n", detail.Name, job.Name)
				} else {
					line = fmt.Sprintf("  ⏳ %s / %s (%s)\n", detail.Name, job.Name, job.Status)
				}
				output.WriteString(line)
			}

			if detail.Status != "completed" {
				allDone = false
			}
		}

		percent := 0
		if totalJobs > 0 {
			percent = completedJobs * 100 / totalJobs
		}

		if currentState != lastState {
			if !firstPrint {
				linesToClear := totalJobs + 1
				for i := 0; i < linesToClear; i++ {
					fmt.Print("\033[A\033[2K")
				}
			}
			firstPrint = false

			printInfo(fmt.Sprintf("Progress: %d/%d (%d%%)", completedJobs, totalJobs, percent))
			fmt.Print(output.String())
			lastState = currentState
		}

		if failFast && hasFailure {
			fmt.Println()
			printWarn("Failure detected, exiting early (--fail-fast)")
			return true, nil
		}

		if allDone {
			break
		}

		time.Sleep(5 * time.Second)
	}
	fmt.Println()

	return hasFailure, nil
}

func showResults(runIDs []int, ctx *Context) bool {
	allSuccess := true

	for _, runID := range runIDs {
		detail, err := getRunDetail(runID)
		if err != nil {
			printError(fmt.Sprintf("Could not get run details for %d", runID))
			continue
		}

		fmt.Println("════════════════════════════════════════════════════════════════")
		if detail.Conclusion == "success" {
			printSuccess(fmt.Sprintf("✅ %s PASSED", detail.Name))
		} else {
			printError(fmt.Sprintf("❌ %s FAILED", detail.Name))
			allSuccess = false
		}
		fmt.Println("════════════════════════════════════════════════════════════════")
		fmt.Println()

		printInfo("Jobs:")
		var failedJobs []Job
		for _, job := range detail.Jobs {
			var icon string
			switch job.Conclusion {
			case "success":
				icon = "✅"
			case "failure":
				icon = "❌"
			case "skipped":
				icon = "⏭️ "
			default:
				icon = "⏳"
			}

			if job.Conclusion == "failure" {
				failedJobs = append(failedJobs, job)
			}
			fmt.Printf("  %s %s\n", icon, job.Name)
		}
		fmt.Println()

		// The failure itself, not a command that would show it.
		for _, job := range failedJobs {
			excerpt := jobFailureLog(runID, job.DatabaseID)
			if excerpt == "" {
				continue
			}
			printError(fmt.Sprintf("❌ %s", job.Name))
			fmt.Println(excerpt)
			fmt.Println()
		}

		fmt.Printf("     Run:  %s\n", detail.URL)

		if detail.Conclusion != "success" {
			fmt.Println()
			printWarn("Full logs:")
			fmt.Printf("  gh run view %d --log-failed\n", runID)
			fmt.Println()
		}
	}

	printInfo("Links:")
	fmt.Printf("  Commit:  %s\n", ctx.CommitURL)
	if ctx.PRURL != "" {
		fmt.Printf("      PR:  %s\n", ctx.PRURL)
	}
	fmt.Println()

	return allSuccess
=======
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
		newDispatchCmd(),
	)
	return rootCmd
>>>>>>> origin/master
}

// errorLineMarkers are what a runner puts in front of the line that actually
// failed. A job log is tens of thousands of lines and the failure is a handful
// of them, so printing the whole thing is the same as printing none of it.
var errorLineMarkers = []string{"##[error]", "FAIL", "Error:", "error:", "panic:"}

// maxLogLines bounds one job's excerpt. A wall of output scrolls the summary --
// the run and job links below it are what a longer read starts from.
const maxLogLines = 40

// jobFailureLog returns the failing steps' output for one job, trimmed to the
// lines that carry the error. Printing this is the whole point: a reader who
// has to run a second command to see WHY a build failed will reach for the raw
// API and hand-roll a poll loop around it.
func jobFailureLog(runID, jobID int) string {
	out, err := runCommand("gh", "run", "view", strconv.Itoa(runID), "--log-failed", "--job", strconv.Itoa(jobID))
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var errorLines []string
	for _, line := range lines {
		for _, marker := range errorLineMarkers {
			if strings.Contains(line, marker) {
				errorLines = append(errorLines, line)
				break
			}
		}
	}

	// No marker matched, so the failure is shaped in a way this does not know:
	// fall back to the tail, where a build that died usually says why.
	if len(errorLines) == 0 {
		errorLines = lines
	}
	if len(errorLines) > maxLogLines {
		errorLines = errorLines[len(errorLines)-maxLogLines:]
	}
	return strings.Join(errorLines, "\n")
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
