package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command layer is exercised against the same stub `gh` and `git` the bats
// suite uses. Both are found through PATH, so no production code needs a seam
// for tests to reach: the binary shells out exactly as it does in real use.

func mocksDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("tests", "mocks"))
	require.NoError(t, err)
	for _, name := range []string{"gh", "git"} {
		require.NoError(t, os.Chmod(filepath.Join(dir, name), 0o755))
	}
	return dir
}

// runCLI drives the real command tree and returns everything it printed.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("PATH", mocksDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	repoFlag = "" // the flag binds a global, so a previous case must not leak

	r, w, err := os.Pipe()
	require.NoError(t, err)
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w

	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(w)
	cmd.SetErr(w)
	runErr := cmd.Execute()

	os.Stdout, os.Stderr = oldOut, oldErr
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(out), runErr
}

// finishedRun is the fixture every query case runs against: one completed run
// with a passing job and a failing one.
func finishedRun(t *testing.T) {
	t.Helper()
	t.Setenv("MOCK_API_RUNS_JSON", `{"workflow_runs": [
		{"id": 12345, "name": "CI", "status": "completed", "conclusion": "failure",
		 "head_branch": "claude/thing", "head_sha": "abc123def456789", "event": "push",
		 "run_number": 7, "run_attempt": 1, "created_at": "2026-06-01T00:00:00Z",
		 "updated_at": "2026-06-01T00:03:00Z", "actor": {"login": "someone"},
		 "html_url": "https://github.com/test-owner/test-repo/actions/runs/12345"}]}`)
	t.Setenv("MOCK_API_RUN_JSON", `{"id": 12345, "name": "CI", "status": "completed",
		"conclusion": "failure", "head_branch": "claude/thing", "head_sha": "abc123def456789",
		"event": "push", "run_number": 7, "run_attempt": 1,
		"created_at": "2026-06-01T00:00:00Z", "updated_at": "2026-06-01T00:03:00Z",
		"actor": {"login": "someone"},
		"html_url": "https://github.com/test-owner/test-repo/actions/runs/12345"}`)
	t.Setenv("MOCK_API_JOBS_JSON", `{"jobs": [
		{"id": 111, "run_id": 12345, "name": "build", "status": "completed", "conclusion": "success",
		 "started_at": "2026-06-01T00:00:10Z", "completed_at": "2026-06-01T00:01:10Z",
		 "runner_name": "ubuntu-latest",
		 "steps": [{"number": 1, "name": "Set up job", "status": "completed", "conclusion": "success"}]},
		{"id": 222, "run_id": 12345, "name": "test", "status": "completed", "conclusion": "failure",
		 "started_at": "2026-06-01T00:01:00Z", "completed_at": "2026-06-01T00:03:00Z",
		 "runner_name": "ubuntu-latest",
		 "steps": [{"number": 2, "name": "Run tests", "status": "completed", "conclusion": "failure"}]}]}`)
}

func TestRunsListsAndFiltersWorkflowRuns(t *testing.T) {
	finishedRun(t)

	out, err := runCLI(t, "runs")
	require.NoError(t, err)
	assert.Contains(t, out, "RUN-ID")
	assert.Contains(t, out, "12345")
	assert.Contains(t, out, "claude/thing")

	out, err = runCLI(t, "runs", "--branch", "claude/thing", "--status", "failure", "--limit", "5", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"id": 12345`)
}

func TestRunsSaysSoWhenNothingMatches(t *testing.T) {
	t.Setenv("MOCK_API_RUNS_JSON", `{"workflow_runs": []}`)
	out, err := runCLI(t, "runs")
	require.NoError(t, err)
	assert.Contains(t, out, "No workflow runs match")
}

func TestViewShowsJobsStepsAndTimings(t *testing.T) {
	finishedRun(t)

	out, err := runCLI(t, "view", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "run 12345")
	assert.Contains(t, out, "build")
	assert.Contains(t, out, "Run tests")
	assert.Contains(t, out, "1m00s", "a job's elapsed time is part of the summary")
	assert.NotContains(t, out, "gh run ", "the tool must never point at the command it replaces")

	out, err = runCLI(t, "view", "12345", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"jobs"`)
}

// With no run ID the run is resolved from the commit that is checked out.
func TestViewResolvesTheRunForTheCurrentCommit(t *testing.T) {
	finishedRun(t)
	out, err := runCLI(t, "view")
	require.NoError(t, err)
	assert.Contains(t, out, "run 12345")
}

func TestJobsListsAndFiltersToFailures(t *testing.T) {
	finishedRun(t)

	out, err := runCLI(t, "jobs", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "build")
	assert.Contains(t, out, "test")

	out, err = runCLI(t, "jobs", "12345", "--failed")
	require.NoError(t, err)
	assert.Contains(t, out, "test")
	assert.NotContains(t, out, "build")

	out, err = runCLI(t, "jobs", "12345", "--failed", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"id": 222`)
}

func TestLogCleansEachLineAndRawKeepsIt(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z Hello from CI\n"+
		"2026-06-01T02:05:02.7654321Z ##[error]it broke\n")

	out, err := runCLI(t, "log", "12345", "--plain")
	require.NoError(t, err)
	assert.Contains(t, out, "Hello from CI")
	assert.Contains(t, out, "it broke")
	assert.NotContains(t, out, "2026-06-01T02:05:01")
	assert.NotContains(t, out, "##[error]")

	out, err = runCLI(t, "log", "12345", "--raw", "--plain")
	require.NoError(t, err)
	assert.Contains(t, out, "2026-06-01T02:05:01")
	assert.Contains(t, out, "##[error]")
}

func TestLogNarrowsByJobAndTail(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z first\n"+
		"2026-06-01T02:05:02.1234567Z second\n")

	out, err := runCLI(t, "log", "12345", "--job", "test", "--plain")
	require.NoError(t, err)
	assert.Contains(t, out, "══ test")
	assert.NotContains(t, out, "══ build")

	out, err = runCLI(t, "log", "12345", "--job", "222", "--tail", "1", "--plain", "--no-headers")
	require.NoError(t, err)
	assert.NotContains(t, out, "first")
	assert.Contains(t, out, "second")
}

func TestLogFailedKeepsOnlyTheFailingJob(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z a line\n")

	out, err := runCLI(t, "log", "12345", "--failed", "--plain")
	require.NoError(t, err)
	assert.Contains(t, out, "══ test")
	assert.NotContains(t, out, "══ build")
}

func TestLogRejectsASelectorMatchingNoJob(t *testing.T) {
	finishedRun(t)
	_, err := runCLI(t, "log", "12345", "--job", "nosuchjob")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no job")
}

// The archive that carries step boundaries exists only for a finished run, so a
// --step request against the per-job fallback must say why it cannot be served.
func TestLogExplainsWhyStepLogsAreUnavailable(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z a line\n")

	_, err := runCLI(t, "log", "12345", "--step", "2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step-level logs are not available")
}

func TestLogSaysSoWhenNoLogExistsYet(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "")

	out, err := runCLI(t, "log", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "No logs available")
}

func TestGrepReportsMatchesWithTheirSection(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z all good\n"+
		"2026-06-01T02:05:02.7654321Z ##[error]FAIL: TestThing\n")

	out, err := runCLI(t, "grep", "--plain", "FAIL", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "FAIL: TestThing")
	assert.Contains(t, out, "══ build")

	out, err = runCLI(t, "grep", "--plain", "-i", "-C", "1", "fail", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "all good", "context lines accompany the match")
}

func TestGrepCountAndListAndJSON(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z boom\n"+
		"2026-06-01T02:05:02.7654321Z boom again\n")

	out, err := runCLI(t, "grep", "--count", "boom", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "build: 2")

	out, err = runCLI(t, "grep", "-l", "boom", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "build")

	out, err = runCLI(t, "grep", "--json", "boom", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, `"job": "build"`)
}

// grep exits non-zero on no match, the way grep itself does, and says nothing.
func TestGrepExitsNonZeroWithoutNoiseWhenNothingMatches(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z all good\n")

	out, err := runCLI(t, "grep", "--plain", "no-such-text", "12345")
	require.Error(t, err)
	assert.Contains(t, out, "")
	assert.NotContains(t, out, "ERROR")

	_, err = runCLI(t, "grep", "--json", "no-such-text", "12345")
	require.Error(t, err)
	_, err = runCLI(t, "grep", "--count", "no-such-text", "12345")
	require.Error(t, err)
	_, err = runCLI(t, "grep", "-l", "no-such-text", "12345")
	require.Error(t, err)
}

func TestGrepRejectsAnInvalidPattern(t *testing.T) {
	finishedRun(t)
	_, err := runCLI(t, "grep", "a(", "12345")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pattern")
}

func TestAnnotationsShowTheErrorsThatNeverReachTheLogs(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_API_ANNOTATIONS_JSON", `[{"path": "main.go", "start_line": 12, "end_line": 12,
		"annotation_level": "failure", "title": "vet", "message": "undefined: foo"}]`)

	out, err := runCLI(t, "annotations", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "main.go:12")
	assert.Contains(t, out, "undefined: foo")

	out, err = runCLI(t, "annotations", "12345", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"annotation_level": "failure"`)
}

func TestAnnotationsSaysSoWhenThereAreNone(t *testing.T) {
	finishedRun(t)
	out, err := runCLI(t, "annotations", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "no annotations")
}

func TestArtifactsListsThemAndSaysSoWhenThereAreNone(t *testing.T) {
	finishedRun(t)
	out, err := runCLI(t, "artifacts", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "uploaded no artifacts")

	t.Setenv("MOCK_API_ARTIFACTS_JSON", `{"artifacts": [{"id": 99, "name": "coverage",
		"size_in_bytes": 2048, "expired": false, "created_at": "2026-06-01T00:03:00Z"}]}`)
	out, err = runCLI(t, "artifacts", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "coverage")
	assert.Contains(t, out, "2.0KB")

	out, err = runCLI(t, "artifacts", "12345", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"name": "coverage"`)
}

func TestArtifactsDownloadRejectsANameThatMatchesNothing(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_API_ARTIFACTS_JSON", `{"artifacts": [{"id": 99, "name": "coverage",
		"size_in_bytes": 2048, "expired": false}]}`)

	_, err := runCLI(t, "artifacts", "12345", "--download", "nosuch", "--dir", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no artifact")
}

// An expired artifact is reported rather than silently skipped.
func TestArtifactsDownloadReportsAnExpiredArtifact(t *testing.T) {
	finishedRun(t)
	t.Setenv("MOCK_API_ARTIFACTS_JSON", `{"artifacts": [{"id": 99, "name": "coverage",
		"size_in_bytes": 2048, "expired": true}]}`)

	out, err := runCLI(t, "artifacts", "12345", "--download", "all", "--dir", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, out, "has expired")
}

func TestWorkflowsListsTheRepositoryDefinitions(t *testing.T) {
	t.Setenv("MOCK_API_WORKFLOWS_JSON", `{"workflows": [{"id": 1, "name": "CI",
		"path": ".github/workflows/ci.yml", "state": "active"}]}`)

	out, err := runCLI(t, "workflows")
	require.NoError(t, err)
	assert.Contains(t, out, "ci.yml")
	assert.Contains(t, out, "active")

	out, err = runCLI(t, "workflows", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"path": ".github/workflows/ci.yml"`)
}

func TestCancelAndRerun(t *testing.T) {
	finishedRun(t)

	out, err := runCLI(t, "cancel", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "Cancelled run 12345")

	out, err = runCLI(t, "rerun", "12345")
	require.NoError(t, err)
	assert.Contains(t, out, "Re-ran run 12345")

	out, err = runCLI(t, "rerun", "12345", "--failed")
	require.NoError(t, err)
	assert.Contains(t, out, "failed jobs of run 12345")

	out, err = runCLI(t, "rerun", "12345", "--job", "test")
	require.NoError(t, err)
	assert.Contains(t, out, "job test of run 12345")

	_, err = runCLI(t, "rerun", "12345", "--job", "nosuchjob")
	require.Error(t, err)
}

func TestWaitReportsAPassingRun(t *testing.T) {
	t.Setenv("MOCK_RUN_LIST_JSON", `[{"databaseId": 12345, "status": "completed",
		"conclusion": "success", "name": "CI"}]`)
	t.Setenv("MOCK_RUN_VIEW_JSON", `{"status": "completed", "conclusion": "success", "name": "CI",
		"url": "https://github.com/test-owner/test-repo/actions/runs/12345",
		"jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]}`)

	out, err := runCLI(t)
	require.NoError(t, err)
	assert.Contains(t, out, "PASSED")
	assert.Contains(t, out, "Progress: 1/1 (100%)")
}

// A failing wait points the reader at this tool's own log commands.
func TestWaitOnAFailingRunPointsAtTheReplacementCommands(t *testing.T) {
	t.Setenv("MOCK_RUN_LIST_JSON", `[{"databaseId": 12345, "status": "completed",
		"conclusion": "failure", "name": "CI"}]`)
	t.Setenv("MOCK_RUN_VIEW_JSON", `{"status": "completed", "conclusion": "failure", "name": "CI",
		"url": "https://github.com/test-owner/test-repo/actions/runs/12345",
		"jobs": [{"name": "test", "status": "completed", "conclusion": "failure", "databaseId": 222}]}`)

	out, err := runCLI(t, "watch")
	require.Error(t, err)
	assert.Contains(t, out, "FAILED")
	assert.Contains(t, out, "gh wait-ci log 12345 --failed")
	assert.Contains(t, out, "gh wait-ci grep")
	assert.Contains(t, out, "gh wait-ci annotations 12345")
	assert.NotContains(t, out, "gh run ")
}

func TestWaitWithLogsStreamsTheJobLog(t *testing.T) {
	t.Setenv("MOCK_RUN_LIST_JSON", `[{"databaseId": 12345, "status": "completed",
		"conclusion": "success", "name": "CI"}]`)
	t.Setenv("MOCK_RUN_VIEW_JSON", `{"status": "completed", "conclusion": "success", "name": "CI",
		"url": "https://github.com/test-owner/test-repo/actions/runs/12345",
		"jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111,
		 "steps": [{"number": 1, "name": "Set up job", "status": "completed", "conclusion": "success"}]}]}`)
	t.Setenv("MOCK_JOB_LOG", "2026-06-01T02:05:01.1234567Z Hello from CI\n")

	out, err := runCLI(t, "--logs")
	require.NoError(t, err)
	assert.Contains(t, out, "Hello from CI")
	assert.Contains(t, out, "Set up job")
	assert.Contains(t, out, "PASSED")
}

func TestWaitFailFastStopsAtTheFirstFailure(t *testing.T) {
	t.Setenv("MOCK_RUN_LIST_JSON", `[{"databaseId": 12345, "status": "completed",
		"conclusion": "failure", "name": "CI"}]`)
	t.Setenv("MOCK_RUN_VIEW_JSON", `{"status": "completed", "conclusion": "failure", "name": "CI",
		"url": "https://github.com/test-owner/test-repo/actions/runs/12345",
		"jobs": [{"name": "test", "status": "completed", "conclusion": "failure", "databaseId": 222}]}`)

	out, err := runCLI(t, "--fail-fast")
	require.Error(t, err)
	assert.Contains(t, out, "--fail-fast")
}

func TestRepoFlagTargetsAnotherRepository(t *testing.T) {
	t.Setenv("MOCK_API_REPO_JSON", `{"default_branch": "main"}`)
	t.Setenv("MOCK_API_COMMITS_JSON", `{"sha": "def456abc789012"}`)
	t.Setenv("MOCK_API_RUNS_JSON", `{"workflow_runs": [{"id": 99999, "name": "CI",
		"status": "completed", "conclusion": "success", "head_branch": "main",
		"head_sha": "def456abc789012", "event": "push"}]}`)

	out, err := runCLI(t, "runs", "-R", "other-owner/other-repo")
	require.NoError(t, err)
	assert.Contains(t, out, "99999")
}

func TestUnknownFlagIsReported(t *testing.T) {
	out, err := runCLI(t, "--bogus")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(out+err.Error()), "unknown flag")
}
