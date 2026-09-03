package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// nullTime tolerates the three shapes GitHub uses for an optional timestamp:
// a JSON null, an empty string, and an RFC3339 string.
type nullTime struct {
	time.Time
	Valid bool
}

func (n *nullTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		n.Valid = false
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		n.Valid = false
		return nil
	}
	n.Time = t
	n.Valid = true
	return nil
}

func (n nullTime) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Time)
}

type apiActor struct {
	Login string `json:"login"`
}

// apiRun is a workflow run as the REST API reports it.
type apiRun struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	DisplayTitle string   `json:"display_title"`
	HeadBranch   string   `json:"head_branch"`
	HeadSHA      string   `json:"head_sha"`
	Path         string   `json:"path"`
	Event        string   `json:"event"`
	Status       string   `json:"status"`
	Conclusion   string   `json:"conclusion"`
	RunNumber    int      `json:"run_number"`
	RunAttempt   int      `json:"run_attempt"`
	CreatedAt    nullTime `json:"created_at"`
	UpdatedAt    nullTime `json:"updated_at"`
	HTMLURL      string   `json:"html_url"`
	Actor        apiActor `json:"actor"`
}

// apiStep is one step of a job.
type apiStep struct {
	Number      int      `json:"number"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Conclusion  string   `json:"conclusion"`
	StartedAt   nullTime `json:"started_at"`
	CompletedAt nullTime `json:"completed_at"`
}

// apiJob is one job of a workflow run. Its ID doubles as the check-run ID, which
// is what the annotations endpoint takes.
type apiJob struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id"`
	RunAttempt  int       `json:"run_attempt"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   nullTime  `json:"started_at"`
	CompletedAt nullTime  `json:"completed_at"`
	HTMLURL     string    `json:"html_url"`
	RunnerName  string    `json:"runner_name"`
	Labels      []string  `json:"labels"`
	Steps       []apiStep `json:"steps"`
}

// apiAnnotation is one check-run annotation: the file-and-line errors and
// warnings GitHub renders at the top of a run page.
type apiAnnotation struct {
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	AnnotationLevel string `json:"annotation_level"`
	Title           string `json:"title"`
	Message         string `json:"message"`
	RawDetails      string `json:"raw_details"`
}

// apiArtifact is one uploaded artifact of a run.
type apiArtifact struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	SizeInBytes        int64    `json:"size_in_bytes"`
	Expired            bool     `json:"expired"`
	CreatedAt          nullTime `json:"created_at"`
	ExpiresAt          nullTime `json:"expires_at"`
	ArchiveDownloadURL string   `json:"archive_download_url"`
}

// apiWorkflow is one workflow definition in the repository.
type apiWorkflow struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// ghAPIBytes runs `gh api` and returns raw stdout. Nothing is trimmed, because
// callers include ones reading a zip archive and ones doing byte-offset
// bookkeeping over a log.
func ghAPIBytes(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", append([]string{"api"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh api %s: %s: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ghAPIJSON runs `gh api <path>` and decodes the response into v.
func ghAPIJSON(path string, v any) error {
	out, err := ghAPIBytes(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("could not parse response from %s: %w", path, err)
	}
	return nil
}

// ghAPIPost issues a POST and discards the (usually empty) body.
func ghAPIPost(path string) error {
	_, err := ghAPIBytes("--method", "POST", path)
	return err
}

// fetchRun reads one workflow run.
func fetchRun(repo string, runID int64) (*apiRun, error) {
	var r apiRun
	if err := ghAPIJSON(fmt.Sprintf("repos/%s/actions/runs/%d", repo, runID), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// fetchJobs reads every job of a run. attempt 0 means the latest attempt.
func fetchJobs(repo string, runID int64, attempt int) ([]apiJob, error) {
	path := fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100&filter=latest", repo, runID)
	if attempt > 0 {
		path = fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", repo, runID, attempt)
	}
	var resp struct {
		Jobs []apiJob `json:"jobs"`
	}
	if err := ghAPIJSON(path, &resp); err != nil {
		return nil, err
	}
	return resp.Jobs, nil
}

// fetchRuns lists workflow runs, newest first. Every filter is optional.
func fetchRuns(repo string, f runFilter) ([]apiRun, error) {
	q := []string{fmt.Sprintf("per_page=%d", f.limitOrDefault())}
	if f.Branch != "" {
		q = append(q, "branch="+urlValue(f.Branch))
	}
	if f.Event != "" {
		q = append(q, "event="+urlValue(f.Event))
	}
	if f.Status != "" {
		q = append(q, "status="+urlValue(f.Status))
	}
	if f.Actor != "" {
		q = append(q, "actor="+urlValue(f.Actor))
	}
	if f.Commit != "" {
		q = append(q, "head_sha="+urlValue(f.Commit))
	}

	base := fmt.Sprintf("repos/%s/actions/runs", repo)
	if f.Workflow != "" {
		base = fmt.Sprintf("repos/%s/actions/workflows/%s/runs", repo, urlValue(f.Workflow))
	}

	var resp struct {
		Runs []apiRun `json:"workflow_runs"`
	}
	if err := ghAPIJSON(base+"?"+strings.Join(q, "&"), &resp); err != nil {
		return nil, err
	}
	return resp.Runs, nil
}

// fetchAnnotations reads the check-run annotations for a job. A job's ID is also
// its check-run ID, which is what makes this reachable without a separate lookup.
func fetchAnnotations(repo string, jobID int64) ([]apiAnnotation, error) {
	var out []apiAnnotation
	if err := ghAPIJSON(fmt.Sprintf("repos/%s/check-runs/%d/annotations?per_page=100", repo, jobID), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// fetchArtifacts lists a run's artifacts.
func fetchArtifacts(repo string, runID int64) ([]apiArtifact, error) {
	var resp struct {
		Artifacts []apiArtifact `json:"artifacts"`
	}
	if err := ghAPIJSON(fmt.Sprintf("repos/%s/actions/runs/%d/artifacts?per_page=100", repo, runID), &resp); err != nil {
		return nil, err
	}
	return resp.Artifacts, nil
}

// fetchWorkflows lists the repository's workflow definitions.
func fetchWorkflows(repo string) ([]apiWorkflow, error) {
	var resp struct {
		Workflows []apiWorkflow `json:"workflows"`
	}
	if err := ghAPIJSON(fmt.Sprintf("repos/%s/actions/workflows?per_page=100", repo), &resp); err != nil {
		return nil, err
	}
	return resp.Workflows, nil
}

// runFilter carries the optional filters for a run listing.
type runFilter struct {
	Branch   string
	Event    string
	Status   string
	Actor    string
	Commit   string
	Workflow string
	Limit    int
}

func (f runFilter) limitOrDefault() int {
	if f.Limit <= 0 {
		return 20
	}
	if f.Limit > 100 {
		return 100
	}
	return f.Limit
}

// urlValue percent-encodes the characters that appear in the filter values this
// tool passes (a branch name, a workflow file name, a login).
func urlValue(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
