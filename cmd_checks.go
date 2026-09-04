package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// checksReport is what `checks` prints, and what --json emits.
type checksReport struct {
	Repo      string            `json:"repo"`
	SHA       string            `json:"sha"`
	State     string            `json:"state"`
	Statuses  []apiCommitStatus `json:"statuses"`
	CheckRuns []apiCheckRun     `json:"check_runs"`
}

// resolveChecksCommit picks the commit `checks` reads. Without --repo the local
// HEAD is the answer. With --repo and no commit there is nothing local to use,
// so the repository's default branch supplies one.
func resolveChecksCommit(repo, sha string) (string, error) {
	if commit := resolveCommit(repo, sha); commit != "" {
		return commit, nil
	}
	var info RemoteRepoInfo
	if err := ghAPIJSON("repos/"+repo, &info); err != nil {
		return "", fmt.Errorf("could not determine which commit to read: %w", err)
	}
	var c RemoteCommitInfo
	if err := ghAPIJSON(fmt.Sprintf("repos/%s/commits/%s", repo, info.DefaultBranch), &c); err != nil {
		return "", fmt.Errorf("could not determine which commit to read: %w", err)
	}
	return c.SHA, nil
}

// stateIcon renders a commit-status state, which has its own vocabulary.
func stateIcon(state string) string {
	switch state {
	case "success":
		return "✅"
	case "failure", "error":
		return "❌"
	case "pending":
		return "⏳"
	}
	return "⏳"
}

func newChecksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checks [commit-ish]",
		Short: "Every check on a commit: check runs AND commit statuses",
		Long: "Show the whole check surface of one commit.\n\n" +
			"A workflow run is not the only thing that gates a merge. An external app\n" +
			"posts a check run, and the legacy status API carries a commit status that\n" +
			"appears in no run listing and no check-run listing. A required status such\n" +
			"as all-builds is one of those, so `runs` and `view` cannot report it.\n\n" +
			"Without a commit this reads the commit that is checked out.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo()
			if err != nil {
				return err
			}
			sha, _ := cmd.Flags().GetString("sha")
			if len(args) > 0 {
				sha = args[0]
			}
			commit, err := resolveChecksCommit(repo, sha)
			if err != nil {
				return err
			}

			// The two surfaces need different permissions, and a token that
			// reads one often cannot read the other: the Checks API is a
			// GitHub App scope, so a fine-grained PAT answers it with 403
			// while the status API answers normally. Failing the whole
			// command on that would hide the commit statuses, which are the
			// half that names the required gate. Report each loss instead.
			combined, statusErr := fetchCombinedStatus(repo, commit)
			if statusErr != nil {
				combined = &apiCombinedStatus{State: "unknown"}
				printWarn("Commit statuses are UNREADABLE with this token, and are missing below:")
				fmt.Fprintf(os.Stderr, "  %v\n", statusErr)
			}
			runs, runsErr := fetchCheckRuns(repo, commit)
			if runsErr != nil {
				printWarn("Check runs are UNREADABLE with this token, and are missing below:")
				fmt.Fprintf(os.Stderr, "  %v\n", runsErr)
				printWarn("The Checks API is a GitHub App scope; a fine-grained PAT cannot read it.")
			}
			if statusErr != nil && runsErr != nil {
				return fmt.Errorf("no check surface of %s could be read", shortSHA(commit))
			}

			if failedOnly, _ := cmd.Flags().GetBool("failed"); failedOnly {
				var keptStatus []apiCommitStatus
				for _, s := range combined.Statuses {
					if s.State != "success" {
						keptStatus = append(keptStatus, s)
					}
				}
				combined.Statuses = keptStatus
				var keptRuns []apiCheckRun
				for _, r := range runs {
					if r.Status != "completed" || (r.Conclusion != "success" && r.Conclusion != "skipped" && r.Conclusion != "neutral") {
						keptRuns = append(keptRuns, r)
					}
				}
				runs = keptRuns
			}

			report := checksReport{Repo: repo, SHA: commit, State: combined.State, Statuses: combined.Statuses, CheckRuns: runs}
			if report.Statuses == nil {
				report.Statuses = []apiCommitStatus{}
			}
			if report.CheckRuns == nil {
				report.CheckRuns = []apiCheckRun{}
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return emitJSON(report)
			}

			printInfo(fmt.Sprintf("%s %s  (rollup: %s)", stateIcon(combined.State), shortSHA(commit), combined.State))
			fmt.Printf("  Repository: %s\n", repo)
			fmt.Println()

			if len(report.CheckRuns) > 0 {
				rows := make([][]string, 0, len(report.CheckRuns))
				for _, r := range report.CheckRuns {
					rows = append(rows, []string{
						statusIcon(r.Status, r.Conclusion),
						r.Name,
						stateWord(r.Status, r.Conclusion),
						r.App.Slug,
						duration(r.StartedAt, r.CompletedAt),
					})
				}
				printTable([]string{"", "CHECK RUN", "STATE", "APP", "TIME"}, rows)
				fmt.Println()
			}

			if len(report.Statuses) > 0 {
				rows := make([][]string, 0, len(report.Statuses))
				for _, s := range report.Statuses {
					rows = append(rows, []string{
						stateIcon(s.State),
						s.Context,
						s.State,
						s.Creator.Login,
						strings.TrimSpace(s.Description),
					})
				}
				printTable([]string{"", "COMMIT STATUS", "STATE", "POSTED BY", "DESCRIPTION"}, rows)
				fmt.Println()
			}

			if len(report.CheckRuns) == 0 && len(report.Statuses) == 0 && statusErr == nil && runsErr == nil {
				printWarn(fmt.Sprintf("No checks reported on %s.", shortSHA(commit)))
			}
			return nil
		},
	}
	cmd.Flags().StringP("sha", "s", "", "Commit to read (full or partial), instead of the checked-out one")
	cmd.Flags().Bool("failed", false, "Show only what did not succeed")
	cmd.Flags().Bool("json", false, "Emit the whole report as JSON")
	return cmd
}

func newDispatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatch <workflow>",
		Short: "Start a workflow_dispatch run",
		Long: "Start a run of a workflow that declares a workflow_dispatch trigger.\n" +
			"The workflow is its file name (ci.yml) or its numeric ID.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo()
			if err != nil {
				return err
			}
			ref, _ := cmd.Flags().GetString("ref")
			if ref == "" {
				ref, err = runCommand("git", "branch", "--show-current")
				if err != nil || ref == "" {
					return fmt.Errorf("could not determine which ref to dispatch on; pass --ref")
				}
			}
			raw, _ := cmd.Flags().GetStringArray("input")
			inputs := map[string]string{}
			for _, kv := range raw {
				name, value, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("--input takes name=value, got %q", kv)
				}
				inputs[name] = value
			}
			if err := dispatchWorkflow(repo, args[0], ref, inputs); err != nil {
				return err
			}
			printSuccess(fmt.Sprintf("Dispatched %s on %s", args[0], ref))
			printInfo("The run takes a moment to appear. Find it with:")
			fmt.Printf("  gh wait-ci runs --workflow %s --branch %s\n", args[0], ref)
			return nil
		},
	}
	cmd.Flags().String("ref", "", "Branch or tag to run on (default: the current branch)")
	cmd.Flags().StringArray("input", nil, "A workflow input, as name=value (repeatable)")
	return cmd
}
