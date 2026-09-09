package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// tsRe matches the leading RFC3339Nano timestamp that GitHub prefixes onto each
// raw job-log line, e.g. "2026-06-01T02:05:01.1234567Z ". We strip it so the
// streamed output reads like the live log view on github.com.
var tsRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z `)

// jobLogState tracks how much of a job's log has already been printed and which
// step transitions have already been announced, so each poll only emits what is
// new.
type jobLogState struct {
	label    string
	printed  int            // byte offset (always at a line boundary) already emitted
	stepSeen map[int]string // step number -> last status we printed
	finished bool           // final log + conclusion banner printed
}

// jobRef pairs a job with the name of the run it belongs to.
type jobRef struct {
	runName string
	job     Job
}

// fetchJobLog downloads the plain-text log for a single job via the REST API.
//
// IMPORTANT: GitHub only makes a job's log available once the job COMPLETES.
// While a job is in progress this endpoint 302-redirects to a storage blob that
// does not exist yet (HTTP 404), so gh exits non-zero and we return an error,
// which the caller treats as "not available yet". There is no token-accessible
// API for a job's in-progress log content -- the live per-line view on
// github.com is served from the browser's authenticated web session, not a
// token-accessible endpoint. We therefore stream step-by-step progress live and
// print each job's full log the moment it finishes.
//
// We deliberately avoid runCommand here because it trims surrounding whitespace,
// which would corrupt the byte-offset bookkeeping used for deltas.
func fetchJobLog(repo string, jobID int) (string, error) {
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// cleanLogLine strips the leading byte-order mark and timestamp and normalizes
// GitHub workflow command markers (##[group], ##[endgroup], ##[error], ...) into
// something readable in a terminal. The second return value is false when the
// line should be dropped entirely (e.g. ##[endgroup]).
func cleanLogLine(line string) (string, bool) {
	line = strings.TrimPrefix(line, "\ufeff") // BOM at the very start of the log
	line = strings.TrimRight(line, "\r")
	line = tsRe.ReplaceAllString(line, "")

	switch {
	case strings.HasPrefix(line, "##[group]"):
		return "‣ " + strings.TrimPrefix(line, "##[group]"), true
	case strings.HasPrefix(line, "##[endgroup]"):
		return "", false
	case strings.HasPrefix(line, "##[error]"):
		return colorRed + strings.TrimPrefix(line, "##[error]") + colorReset, true
	case strings.HasPrefix(line, "##[warning]"):
		return colorYellow + strings.TrimPrefix(line, "##[warning]") + colorReset, true
	case strings.HasPrefix(line, "##[notice]"):
		return strings.TrimPrefix(line, "##[notice]"), true
	case strings.HasPrefix(line, "##[command]"):
		return colorBlue + strings.TrimPrefix(line, "##[command]") + colorReset, true
	case strings.HasPrefix(line, "##[section]"):
		return strings.TrimPrefix(line, "##[section]"), true
	}
	return line, true
}

// tag prints a line with the optional job-name prefix used to tell concurrent
// jobs apart.
func tag(label, line string, prefix bool) {
	if prefix {
		fmt.Printf("%s%s%s │ %s\n", colorBlue, label, colorReset, line)
	} else {
		fmt.Println(line)
	}
}

// printLogLine prints a single cleaned log line.
func printLogLine(label, line string, prefix bool) {
	cleaned, ok := cleanLogLine(line)
	if !ok {
		return
	}
	tag(label, cleaned, prefix)
}

// printStep announces a step starting or finishing, giving live feedback while
// a job runs (the only live signal GitHub exposes to a token before the job's
// log becomes downloadable).
func printStep(label string, step Step, prefix bool) {
	var icon string
	switch {
	case step.Status == "in_progress":
		icon = "▷"
	case step.Conclusion == "success":
		icon = "✓"
	case step.Conclusion == "skipped":
		icon = "·"
	case step.Conclusion == "":
		return // queued/pending: nothing to show yet
	default:
		icon = "✗"
	}
	tag(label, fmt.Sprintf("  %s %s", icon, step.Name), prefix)
}

// emitDelta prints the portion of raw that has not been printed yet. When final
// is false only whole lines (through the last newline) are emitted so a partial
// trailing line is never shown; on the final flush everything remaining is
// printed. raw is assumed to be append-only across polls.
func emitDelta(st *jobLogState, raw string, prefix, final bool) {
	if len(raw) < st.printed {
		return // logs only grow; guard against a short read
	}
	chunk := raw[st.printed:]
	if !final {
		nl := strings.LastIndexByte(chunk, '\n')
		if nl < 0 {
			return // no complete new line yet
		}
		chunk = chunk[:nl+1]
	}
	if chunk == "" {
		return
	}
	st.printed += len(chunk)
	for _, line := range strings.Split(strings.TrimRight(chunk, "\n"), "\n") {
		printLogLine(st.label, line, prefix)
	}
}

// printJobBanner announces a job starting or finishing.
func printJobBanner(label, state string) {
	switch state {
	case "started":
		fmt.Printf("\n%s▶ %s%s\n", colorBlue, label, colorReset)
	case "success":
		fmt.Printf("%s✅ %s — passed%s\n", colorGreen, label, colorReset)
	case "skipped":
		fmt.Printf("%s⏭️  %s — skipped%s\n", colorYellow, label, colorReset)
	case "cancelled":
		fmt.Printf("%s⛔ %s — cancelled%s\n", colorRed, label, colorReset)
	default:
		fmt.Printf("%s❌ %s — %s%s\n", colorRed, label, state, colorReset)
	}
}

// streamLogs waits for all runs to finish while showing live step-by-step
// progress and printing each job's full log the moment that job completes. It
// returns whether any job failed. A zero deadline waits forever.
func streamLogs(runIDs []int, ctx *Context, failFast bool, interval time.Duration, deadline time.Time) (bool, error) {
	printInfo("Streaming logs — step progress is live; each job's full log prints when it finishes.")

	states := map[int]*jobLogState{}
	hasFailure := false

	for {
		allDone := true
		var jobs []jobRef

		for _, runID := range runIDs {
			detail, err := getRunDetail(runID)
			if err != nil {
				allDone = false
				continue
			}
			if detail.Status != "completed" {
				allDone = false
			}
			for _, job := range detail.Jobs {
				jobs = append(jobs, jobRef{runName: detail.Name, job: job})
			}
		}

		prefix := len(jobs) > 1

		for _, jr := range jobs {
			job := jr.job

			// No logs or step activity exist until a job actually starts.
			if job.Status != "in_progress" && job.Status != "completed" {
				continue
			}

			st := states[job.DatabaseID]
			if st == nil {
				label := job.Name
				if prefix {
					label = jr.runName + " / " + job.Name
				}
				st = &jobLogState{label: label, stepSeen: map[int]string{}}
				states[job.DatabaseID] = st
				printJobBanner(label, "started")
			}
			if st.finished {
				continue
			}

			// Live step progress: announce each step transition once.
			for _, step := range job.Steps {
				if st.stepSeen[step.Number] == step.Status {
					continue
				}
				st.stepSeen[step.Number] = step.Status
				printStep(st.label, step, prefix)
			}

			completed := job.Status == "completed"
			if completed {
				// The log becomes downloadable only now; print it in full.
				if raw, err := fetchJobLog(ctx.Repo, job.DatabaseID); err == nil {
					emitDelta(st, raw, prefix, true)
				}
				st.finished = true
				switch job.Conclusion {
				case "success", "skipped", "neutral":
				default:
					hasFailure = true
				}
				printJobBanner(st.label, job.Conclusion)
			}
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
