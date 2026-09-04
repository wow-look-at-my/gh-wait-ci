package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
		runsJSON, err := ghCommand("run", "list", "--commit", ctx.Commit,
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

// checkDeadline stops a wait that has run out of time. A zero deadline never
// expires. The error names a run so the caller reads the state it never reached.
func checkDeadline(deadline time.Time, runIDs []int) error {
	if deadline.IsZero() || time.Now().Before(deadline) {
		return nil
	}
	id := 0
	if len(runIDs) > 0 {
		id = runIDs[0]
	}
	return fmt.Errorf("timed out waiting for CI. Read the state it reached with:\n"+
		"  gh wait-ci view %d\n"+
		"Raise the limit with --timeout, or pass --timeout 0 to wait with no limit", id)
}

func getRunDetail(runID int) (*RunDetail, error) {
	runJSON, err := ghCommand("run", "view", strconv.Itoa(runID),
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
// when any job fails. A zero deadline waits forever. Returns (hasFailure, error).
func waitForRuns(runIDs []int, failFast bool, interval time.Duration, deadline time.Time) (bool, error) {
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
				allDone = false
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
					case "cancelled":
						line = fmt.Sprintf("  ⛔ %s / %s (cancelled)\n", detail.Name, job.Name)
						hasFailure = true
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
		if err := checkDeadline(deadline, runIDs); err != nil {
			return hasFailure, err
		}

		time.Sleep(interval)
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
			allSuccess = false
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
		for _, job := range detail.Jobs {
			var icon string
			switch job.Conclusion {
			case "success":
				icon = "✅"
			case "failure":
				icon = "❌"
			case "cancelled":
				icon = "⛔"
			case "skipped":
				icon = "⏭️ "
			default:
				icon = "⏳"
			}

			if job.Conclusion == "failure" {
				fmt.Printf("  %s %s  →  gh wait-ci log %d --job %d\n", icon, job.Name, runID, job.DatabaseID)
			} else {
				fmt.Printf("  %s %s\n", icon, job.Name)
			}
		}
		fmt.Println()

		fmt.Printf("     Run:  %s\n", detail.URL)

		if detail.Conclusion != "success" {
			fmt.Println()
			printWarn("View all failed logs:")
			fmt.Printf("  gh wait-ci log %d --failed\n", runID)
			printWarn("Search them:")
			fmt.Printf("  gh wait-ci grep '<pattern>' %d --failed -C 3\n", runID)
			printWarn("Errors GitHub extracted, which are not in the logs:")
			fmt.Printf("  gh wait-ci annotations %d\n", runID)
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
