package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitChecks is the fixture the `checks` cases run against: one commit
// carrying a check run from an app and two commit statuses, one of them the
// required gate that no Actions endpoint reports.
func commitChecks(t *testing.T) {
	t.Helper()
	t.Setenv("MOCK_API_CHECK_RUNS_JSON", `{"check_runs": [
		{"id": 901, "name": "test", "status": "completed", "conclusion": "success",
		 "started_at": "2026-06-01T00:00:10Z", "completed_at": "2026-06-01T00:01:10Z",
		 "app": {"slug": "github-actions", "name": "GitHub Actions"}},
		{"id": 902, "name": "security-scan", "status": "in_progress", "conclusion": "",
		 "app": {"slug": "some-scanner", "name": "Some Scanner"}}]}`)
	t.Setenv("MOCK_API_COMBINED_STATUS_JSON", `{"state": "pending", "sha": "abc123def456789", "statuses": [
		{"id": 1, "state": "success", "context": "ci/lint", "description": "all good",
		 "creator": {"login": "some-app"}},
		{"id": 2, "state": "pending", "context": "all-builds", "description": "No builds reported yet",
		 "creator": {"login": "required-builds-manager"}}]}`)
}

func TestChecksShowsCheckRunsAndCommitStatuses(t *testing.T) {
	commitChecks(t)

	out, err := runCLI(t, "checks")
	require.NoError(t, err)
	assert.Contains(t, out, "security-scan")
	assert.Contains(t, out, "some-scanner", "a check run names the app that posted it")
	assert.Contains(t, out, "all-builds", "the required commit status is the whole reason this command exists")
	assert.Contains(t, out, "No builds reported yet")
	assert.Contains(t, out, "rollup: pending")
}

// The gate that blocks a merge is a commit status, so --failed must keep it and
// drop everything that passed.
func TestChecksFailedKeepsOnlyWhatIsNotGreen(t *testing.T) {
	commitChecks(t)

	out, err := runCLI(t, "checks", "--failed")
	require.NoError(t, err)
	assert.Contains(t, out, "all-builds")
	assert.Contains(t, out, "security-scan")
	assert.NotContains(t, out, "ci/lint")
	assert.NotContains(t, out, "all good")
}

func TestChecksEmitsJSON(t *testing.T) {
	commitChecks(t)

	out, err := runCLI(t, "checks", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"check_runs"`)
	assert.Contains(t, out, `"statuses"`)
	assert.Contains(t, out, `"all-builds"`)
}

func TestChecksSaysSoWhenNothingReported(t *testing.T) {
	t.Setenv("MOCK_API_CHECK_RUNS_JSON", `{"check_runs": []}`)
	t.Setenv("MOCK_API_COMBINED_STATUS_JSON", `{"state": "pending", "sha": "abc123def456789", "statuses": []}`)

	out, err := runCLI(t, "checks", "--sha", "abc123d")
	require.NoError(t, err)
	assert.Contains(t, out, "No checks reported")
}

// A fine-grained PAT cannot read the Checks API at all, so half this command
// 403s in an ordinary session. The commit statuses are the half that names the
// required gate, so they must still print, and the loss must be stated.
func TestChecksReportsOneUnreadableSurfaceAndPrintsTheOther(t *testing.T) {
	commitChecks(t)
	t.Setenv("MOCK_CHECK_RUNS_403", "1")

	out, err := runCLI(t, "checks")
	require.NoError(t, err, "one unreadable surface must not fail the command")
	assert.Contains(t, out, "Check runs are UNREADABLE")
	assert.Contains(t, out, "GitHub App scope")
	assert.Contains(t, out, "all-builds", "the readable surface still prints")
	assert.NotContains(t, out, "No checks reported", "a 403 is not an empty result")
}

// With --repo and no commit there is no local checkout to read, so the default
// branch has to supply one.
func TestChecksFallsBackToTheDefaultBranchWithRepo(t *testing.T) {
	commitChecks(t)

	out, err := runCLI(t, "checks", "--repo", "test-owner/test-repo")
	require.NoError(t, err)
	assert.Contains(t, out, "all-builds")
}

func TestDispatchPostsTheWorkflowAndItsInputs(t *testing.T) {
	log := filepath.Join(t.TempDir(), "post.log")
	t.Setenv("MOCK_API_POST_LOG", log)

	out, err := runCLI(t, "dispatch", "ci.yml", "--ref", "claude/thing", "--input", "level=debug")
	require.NoError(t, err)
	assert.Contains(t, out, "Dispatched ci.yml on claude/thing")

	posted, readErr := os.ReadFile(log)
	require.NoError(t, readErr)
	assert.Contains(t, string(posted), "repos/test-owner/test-repo/actions/workflows/ci.yml/dispatches")
	assert.Contains(t, string(posted), "ref=claude/thing")
	assert.Contains(t, string(posted), "inputs[level]=debug")
}

// Without --ref the current branch is the ref, so the common case needs no flag.
func TestDispatchDefaultsToTheCurrentBranch(t *testing.T) {
	out, err := runCLI(t, "dispatch", "ci.yml")
	require.NoError(t, err)
	assert.Contains(t, out, "Dispatched ci.yml on main")
}

func TestDispatchRejectsAnInputThatIsNotNameValue(t *testing.T) {
	_, err := runCLI(t, "dispatch", "ci.yml", "--input", "level")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name=value")
}

// An expired deadline stops a wait and says how to read the state it reached.
// An unexpired one, and the zero value, never stop it.
func TestCheckDeadline(t *testing.T) {
	err := checkDeadline(time.Now().Add(-time.Second), []int{12345})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "gh wait-ci view 12345")
	assert.Contains(t, err.Error(), "--timeout")

	require.NoError(t, checkDeadline(time.Now().Add(time.Hour), []int{12345}))
	require.NoError(t, checkDeadline(time.Time{}, nil), "a zero deadline waits with no limit")
}

// A wait that runs out of time must exit non-zero rather than hang, which is the
// whole point of bounding it.
func TestWaitStopsAtTheTimeout(t *testing.T) {
	t.Setenv("MOCK_RUN_VIEW_JSON", `{"status": "in_progress", "conclusion": "", "name": "CI",
		"url": "https://github.com/test-owner/test-repo/actions/runs/12345",
		"jobs": [{"name": "build", "status": "in_progress", "conclusion": "", "databaseId": 111}]}`)

	out, err := runCLI(t, "--timeout", "1ns", "--interval", "1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, out, "Waiting for all runs to complete")
}
