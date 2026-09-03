package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// jobAnnotations groups one job's annotations for display and for --json.
type jobAnnotations struct {
	Job         string          `json:"job"`
	JobID       int64           `json:"job_id"`
	Annotations []apiAnnotation `json:"annotations"`
}

func newAnnotationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "annotations [run-id]",
		Short: "Show the errors and warnings GitHub extracted from a run",
		Long: "Show every check-run annotation of a run: the file-and-line errors and\n" +
			"warnings GitHub surfaces at the top of the run page. A workflow produces\n" +
			"these itself, and they are not part of the job logs, so reading the logs\n" +
			"alone can miss them.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			jobSel, _ := cmd.Flags().GetString("job")
			all := []jobAnnotations{}
			for _, j := range tgt.Jobs {
				if !matchesJob(j, jobSel) {
					continue
				}
				anns, err := fetchAnnotations(tgt.Repo, j.ID)
				if err != nil || len(anns) == 0 {
					continue
				}
				all = append(all, jobAnnotations{Job: j.Name, JobID: j.ID, Annotations: anns})
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return emitJSON(all)
			}
			if len(all) == 0 {
				printInfo(fmt.Sprintf("Run %d has no annotations.", tgt.Run.ID))
				return nil
			}
			for _, ja := range all {
				fmt.Printf("\n%s══ %s%s\n", colorBlue, ja.Job, colorReset)
				for _, a := range ja.Annotations {
					color := colorYellow
					if a.AnnotationLevel == "failure" {
						color = colorRed
					}
					loc := a.Path
					if a.StartLine > 0 {
						loc = fmt.Sprintf("%s:%d", a.Path, a.StartLine)
					}
					fmt.Printf("  %s%s%s %s\n", color, strings.ToUpper(a.AnnotationLevel), colorReset, loc)
					if a.Title != "" {
						fmt.Printf("    %s\n", a.Title)
					}
					for _, line := range strings.Split(strings.TrimRight(a.Message, "\n"), "\n") {
						fmt.Printf("    %s\n", line)
					}
				}
			}
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().StringP("job", "j", "", "Only this job (job ID, or part of its name)")
	cmd.Flags().Bool("json", false, "Emit the annotations as JSON")
	return cmd
}

func newArtifactsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts [run-id]",
		Short: "List, and optionally download, a run's artifacts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			arts, err := fetchArtifacts(tgt.Repo, tgt.Run.ID)
			if err != nil {
				return err
			}
			if download, _ := cmd.Flags().GetString("download"); download != "" {
				dir, _ := cmd.Flags().GetString("dir")
				if dir == "" {
					dir = "."
				}
				return downloadArtifacts(tgt, arts, download, dir)
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				if arts == nil {
					arts = []apiArtifact{}
				}
				return emitJSON(arts)
			}
			if len(arts) == 0 {
				printInfo(fmt.Sprintf("Run %d uploaded no artifacts.", tgt.Run.ID))
				return nil
			}
			rows := make([][]string, 0, len(arts))
			for _, a := range arts {
				state := "available"
				if a.Expired {
					state = "expired"
				}
				rows = append(rows, []string{fmt.Sprintf("%d", a.ID), a.Name, humanSize(a.SizeInBytes), state, relativeAge(a.CreatedAt)})
			}
			printTable([]string{"ARTIFACT-ID", "NAME", "SIZE", "STATE", "AGE"}, rows)
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().String("download", "", "Download artifacts whose name contains this, or 'all'")
	cmd.Flags().String("dir", ".", "Directory to write downloaded artifacts into")
	cmd.Flags().Bool("json", false, "Emit the artifacts as JSON")
	return cmd
}

// downloadArtifacts writes every artifact matching sel into dir as a zip.
func downloadArtifacts(tgt *target, arts []apiArtifact, sel, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	found := 0
	for _, a := range arts {
		if sel != "all" && !strings.Contains(strings.ToLower(a.Name), strings.ToLower(sel)) {
			continue
		}
		if a.Expired {
			printWarn(fmt.Sprintf("%s has expired and cannot be downloaded.", a.Name))
			continue
		}
		data, err := ghAPIBytes(fmt.Sprintf("repos/%s/actions/artifacts/%d/zip", tgt.Repo, a.ID))
		if err != nil {
			return fmt.Errorf("could not download %s: %w", a.Name, err)
		}
		path := filepath.Join(dir, a.Name+".zip")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		printSuccess(fmt.Sprintf("Saved %s (%s)", path, humanSize(int64(len(data)))))
		found++
	}
	if found == 0 {
		return fmt.Errorf("no artifact of run %d matches %q", tgt.Run.ID, sel)
	}
	return nil
}

func newWorkflowsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflows",
		Short: "List the repository's workflows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolveRepo()
			if err != nil {
				return err
			}
			wfs, err := fetchWorkflows(repo)
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return emitJSON(wfs)
			}
			rows := make([][]string, 0, len(wfs))
			for _, w := range wfs {
				rows = append(rows, []string{fmt.Sprintf("%d", w.ID), w.Name, w.Path, w.State})
			}
			printTable([]string{"WORKFLOW-ID", "NAME", "PATH", "STATE"}, rows)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Emit the workflows as JSON")
	return cmd
}

func newCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel [run-id]",
		Short: "Cancel a run",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			if err := ghAPIPost(fmt.Sprintf("repos/%s/actions/runs/%d/cancel", tgt.Repo, tgt.Run.ID)); err != nil {
				return err
			}
			printSuccess(fmt.Sprintf("Cancelled run %d (%s)", tgt.Run.ID, tgt.Run.Name))
			return nil
		},
	}
	addSelectorFlags(cmd)
	return cmd
}

func newRerunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rerun [run-id]",
		Short: "Re-run a run, its failed jobs, or one job",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tgt, err := resolveTarget(selectorFrom(cmd, args))
			if err != nil {
				return err
			}
			path := fmt.Sprintf("repos/%s/actions/runs/%d/rerun", tgt.Repo, tgt.Run.ID)
			what := "run"
			if failedOnly, _ := cmd.Flags().GetBool("failed"); failedOnly {
				path += "-failed-jobs"
				what = "failed jobs of run"
			}
			if job, _ := cmd.Flags().GetString("job"); job != "" {
				var picked *apiJob
				for i := range tgt.Jobs {
					if matchesJob(tgt.Jobs[i], job) {
						picked = &tgt.Jobs[i]
						break
					}
				}
				if picked == nil {
					return fmt.Errorf("no job of run %d matches %q", tgt.Run.ID, job)
				}
				path = fmt.Sprintf("repos/%s/actions/jobs/%d/rerun", tgt.Repo, picked.ID)
				what = "job " + picked.Name + " of run"
			}
			if err := ghAPIPost(path); err != nil {
				return err
			}
			printSuccess(fmt.Sprintf("Re-ran %s %d", what, tgt.Run.ID))
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().Bool("failed", false, "Re-run only the failed jobs")
	cmd.Flags().StringP("job", "j", "", "Re-run only this job (job ID, or part of its name)")
	return cmd
}
