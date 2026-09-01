package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorReset  = "\033[0m"
)

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

func checkGitRepo() error {
	_, err := runCommand("git", "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("not in a git repository")
	}
	return nil
}

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
	unpushed, err := runCommand("git", "log", "@{u}..HEAD", "--oneline")
	if err != nil {
		// If there's no upstream, that's a different error - allow it
		return nil
	}
	if unpushed != "" {
		printWarn("Unpushed commits detected:")
		fmt.Println(unpushed)
		return fmt.Errorf("push your changes first before waiting for CI")
	}
	return nil
}

func getContext() (*Context, error) {
	ctx := &Context{}

	commit, err := runCommand("git", "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("could not get commit: %w", err)
	}
	ctx.Commit = commit

	shortCommit, err := runCommand("git", "rev-parse", "--short", "HEAD")
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

func printContext(ctx *Context) {
	printInfo(fmt.Sprintf("Repository: %s", ctx.Repo))
	printInfo(fmt.Sprintf("Branch: %s", ctx.Branch))
	printInfo(fmt.Sprintf("Commit: %s", ctx.ShortCommit))
	fmt.Println()
}

func getPRInfo(ctx *Context) {
	prJSON, err := runCommand("gh", "pr", "view", "--json", "number,url")
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

func findRuns(ctx *Context, runID string) ([]int, error) {
	if runID != "" {
		id, err := strconv.Atoi(runID)
		if err != nil {
			return nil, fmt.Errorf("invalid run ID: %s", runID)
		}
		printInfo(fmt.Sprintf("Watching specified run: %s", runID))
		return []int{id}, nil
	}

	printInfo(fmt.Sprintf("Finding workflow runs for commit %s...", ctx.ShortCommit))

	var runs []RunInfo
	for i := 1; i <= 5; i++ {
		runsJSON, err := runCommand("gh", "run", "list", "--commit", ctx.Commit,
			"--json", "databaseId,status,conclusion,name", "--limit", "10")
		if err != nil {
			runsJSON = "[]"
		}

		if err := json.Unmarshal([]byte(runsJSON), &runs); err != nil {
			runs = []RunInfo{}
		}

		if len(runs) > 0 {
			break
		}

		if i < 5 {
			printWarn(fmt.Sprintf("No runs found yet, waiting 5 seconds... (attempt %d/5)", i))
			time.Sleep(5 * time.Second)
		}
	}

	if len(runs) == 0 {
		return nil, fmt.Errorf("no workflow runs found for commit %s", ctx.ShortCommit)
	}

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
	failFast := flag.Bool("fail-fast", false, "Exit immediately when any job fails")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [run-id]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Wait for GitHub Actions CI to complete and report results.\n")
		fmt.Fprintf(os.Stderr, "If no run-id provided, waits for ALL runs for the current commit.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := checkGitRepo(); err != nil {
		printError(err.Error())
		os.Exit(1)
	}

	if err := checkPushed(); err != nil {
		printError(err.Error())
		os.Exit(1)
	}

	ctx, err := getContext()
	if err != nil {
		printError(err.Error())
		os.Exit(1)
	}

	printContext(ctx)
	getPRInfo(ctx)

	runID := ""
	if flag.NArg() > 0 {
		runID = flag.Arg(0)
	}

	runIDs, err := findRuns(ctx, runID)
	if err != nil {
		printError(err.Error())
		os.Exit(1)
	}

	failFastMode := *failFast
	hasFailure, err := waitForRuns(runIDs, failFastMode)
	if err != nil {
		printError(err.Error())
		os.Exit(1)
	}

	if failFastMode && hasFailure {
		showResults(runIDs, ctx)
		os.Exit(1)
	}

	if !showResults(runIDs, ctx) {
		os.Exit(1)
	}
}
