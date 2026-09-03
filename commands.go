package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// addSelectorFlags registers the flags every run-scoped subcommand shares.
func addSelectorFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("sha", "s", "", "Commit SHA whose run to use (full or partial)")
	cmd.Flags().StringP("workflow", "w", "", "Workflow file name or ID to narrow to")
	cmd.Flags().Int("attempt", 0, "Run attempt number (default: the latest attempt)")
}

// selectorFrom reads the shared selector flags plus the optional positional run ID.
func selectorFrom(cmd *cobra.Command, args []string) selector {
	sel := selector{}
	if len(args) > 0 {
		sel.RunID = args[0]
	}
	sel.SHA, _ = cmd.Flags().GetString("sha")
	sel.Workflow, _ = cmd.Flags().GetString("workflow")
	sel.Attempt, _ = cmd.Flags().GetInt("attempt")
	return sel
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---------------------------------------------------------------- runs

func newRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List workflow runs",
		Long:  "List workflow runs, newest first, with optional filters.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo()
			if err != nil {
				return err
			}
			f := runFilter{}
			f.Branch, _ = cmd.Flags().GetString("branch")
			f.Event, _ = cmd.Flags().GetString("event")
			f.Status, _ = cmd.Flags().GetString("status")
			f.Actor, _ = cmd.Flags().GetString("actor")
			f.Commit, _ = cmd.Flags().GetString("commit")
			f.Workflow, _ = cmd.Flags().GetString("workflow")
			f.Limit, _ = cmd.Flags().GetInt("limit")

			runs, err := fetchRuns(repo, f)
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return emitJSON(runs)
			}
			if len(runs) == 0 {
				printWarn("No workflow runs match.")
				return nil
			}
			rows := make([][]string, 0, len(runs))
			for _, r := range runs {
				rows = append(rows, []string{
					statusIcon(r.Status, r.Conclusion),
					fmt.Sprintf("%d", r.ID),
					stateWord(r.Status, r.Conclusion),
					r.Name,
					r.HeadBranch,
					r.Event,
					shortSHA(r.HeadSHA),
					relativeAge(r.CreatedAt),
				})
			}
			printTable([]string{"", "RUN-ID", "STATE", "WORKFLOW", "BRANCH", "EVENT", "COMMIT", "AGE"}, rows)
			return nil
		},
	}
	cmd.Flags().StringP("branch", "b", "", "Filter by branch")
	cmd.Flags().StringP("event", "e", "", "Filter by triggering event")
	cmd.Flags().String("status", "", "Filter by status or conclusion (queued, in_progress, completed, failure, success, ...)")
	cmd.Flags().StringP("actor", "u", "", "Filter by the user who triggered the run")
	cmd.Flags().StringP("commit", "c", "", "Filter by head commit SHA")
	cmd.Flags().StringP("workflow", "w", "", "Filter by workflow file name or ID")
	cmd.Flags().IntP("limit", "L", 20, "Maximum runs to list (max 100)")
	cmd.Flags().Bool("json", false, "Emit the raw run objects as JSON")
	return cmd
}

// ---------------------------------------------------------------- view

func newViewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view [run-id]",
		Short: "Show a run's jobs, steps, and timings",
		Long:  "Show a run's summary, its jobs, and each job's steps with timings.\nWithout a run ID it resolves the run for the current commit.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return emitJSON(struct {
					Run  apiRun   `json:"run"`
					Jobs []apiJob `json:"jobs"`
				}{tgt.Run, tgt.Jobs})
			}

			r := tgt.Run
			printInfo(fmt.Sprintf("%s %s  (run %d, attempt %d)", statusIcon(r.Status, r.Conclusion), r.Name, r.ID, r.RunAttempt))
			fmt.Printf("  Repository: %s\n", tgt.Repo)
			fmt.Printf("  Branch:     %s\n", r.HeadBranch)
			fmt.Printf("  Commit:     %s\n", shortSHA(r.HeadSHA))
			fmt.Printf("  Event:      %s (%s)\n", r.Event, r.Actor.Login)
			fmt.Printf("  State:      %s\n", stateWord(r.Status, r.Conclusion))
			fmt.Printf("  Elapsed:    %s\n", duration(r.CreatedAt, r.UpdatedAt))
			fmt.Printf("  URL:        %s\n", r.HTMLURL)
			fmt.Println()

			showSteps, _ := cmd.Flags().GetBool("steps")
			for _, j := range tgt.Jobs {
				fmt.Printf("%s %s  (job %d, %s)\n", statusIcon(j.Status, j.Conclusion), j.Name, j.ID, duration(j.StartedAt, j.CompletedAt))
				if !showSteps {
					continue
				}
				for _, s := range j.Steps {
					fmt.Printf("    %s %2d. %-40s %s\n", statusIcon(s.Status, s.Conclusion), s.Number, s.Name, duration(s.StartedAt, s.CompletedAt))
				}
			}
			fmt.Println()
			printInfo("Read the logs with:")
			fmt.Printf("  gh wait-ci log %d --failed\n", r.ID)
			fmt.Printf("  gh wait-ci grep <pattern> %d\n", r.ID)
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().Bool("steps", true, "Show each job's steps")
	cmd.Flags().Bool("json", false, "Emit the run and jobs as JSON")
	return cmd
}

// ---------------------------------------------------------------- jobs

func newJobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs [run-id]",
		Short: "List a run's jobs with their IDs and timings",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			jobs := tgt.Jobs
			if failedOnly, _ := cmd.Flags().GetBool("failed"); failedOnly {
				var kept []apiJob
				for _, j := range jobs {
					if jobFailed(j) {
						kept = append(kept, j)
					}
				}
				jobs = kept
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return emitJSON(jobs)
			}
			if len(jobs) == 0 {
				printWarn("No jobs match.")
				return nil
			}
			rows := make([][]string, 0, len(jobs))
			for _, j := range jobs {
				rows = append(rows, []string{
					statusIcon(j.Status, j.Conclusion),
					fmt.Sprintf("%d", j.ID),
					stateWord(j.Status, j.Conclusion),
					j.Name,
					duration(j.StartedAt, j.CompletedAt),
					j.RunnerName,
				})
			}
			printTable([]string{"", "JOB-ID", "STATE", "NAME", "TIME", "RUNNER"}, rows)
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().Bool("failed", false, "List only jobs that did not succeed")
	cmd.Flags().Bool("json", false, "Emit the jobs as JSON")
	return cmd
}

// ---------------------------------------------------------------- log

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log [run-id]",
		Short: "Print a run's logs, whole or filtered by job and step",
		Long: "Print the logs of a run.\n\n" +
			"With no filter this prints every job's log. --job and --step narrow it,\n" +
			"--failed keeps only what did not succeed, and --raw keeps GitHub's\n" +
			"timestamps and ##[...] markers instead of cleaning them up.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			opt := collectOptions{}
			opt.Job, _ = cmd.Flags().GetString("job")
			opt.Step, _ = cmd.Flags().GetString("step")
			opt.FailedOnly, _ = cmd.Flags().GetBool("failed")

			sections, haveSteps, err := collectSections(tgt.Repo, tgt.Run.ID, tgt.Jobs, opt)
			if err != nil {
				return err
			}
			if len(sections) == 0 {
				printWarn(fmt.Sprintf("No logs available for run %d yet. GitHub publishes a job's log once that job finishes.", tgt.Run.ID))
				return nil
			}

			raw, _ := cmd.Flags().GetBool("raw")
			plain, _ := cmd.Flags().GetBool("plain")
			noHeaders, _ := cmd.Flags().GetBool("no-headers")
			tail, _ := cmd.Flags().GetInt("tail")

			if !haveSteps && opt.Step == "" && !noHeaders {
				printWarn("Step-level logs need a finished run; showing whole-job logs.")
			}

			for _, s := range sections {
				lines := s.Lines
				if tail > 0 && len(lines) > tail {
					lines = lines[len(lines)-tail:]
				}
				if !noHeaders {
					fmt.Printf("\n%s══ %s%s\n", colorBlue, s.Label(), colorReset)
				}
				for _, line := range lines {
					out, ok := renderLogLine(line, raw, plain)
					if !ok {
						continue
					}
					fmt.Println(out)
				}
			}
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().StringP("job", "j", "", "Only this job (job ID, or part of its name)")
	cmd.Flags().String("step", "", "Only this step (step number, or part of its name)")
	cmd.Flags().Bool("failed", false, "Only jobs and steps that did not succeed")
	cmd.Flags().Bool("raw", false, "Keep GitHub's timestamps and ##[...] markers")
	cmd.Flags().Bool("plain", false, "Strip every color escape")
	cmd.Flags().Bool("no-headers", false, "Omit the per-section header lines")
	cmd.Flags().Int("tail", 0, "Print only the last N lines of each section")
	return cmd
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
