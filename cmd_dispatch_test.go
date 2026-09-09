package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postLog points the stub `gh` at a file recording every POST it is handed,
// so what the command actually asked GitHub for is what gets asserted.
func postLog(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "posts")
	t.Setenv("MOCK_API_POST_LOG", path)
	return func() string {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return ""
		}
		require.NoError(t, err)
		return string(b)
	}
}

// Starting a workflow by hand has no other route here: `gh workflow run` is
// refused by policy, so a workflow_dispatch could not be triggered at all.
func TestDispatchPostsTheWorkflowAndRef(t *testing.T) {
	read := postLog(t)

	out, err := runCLI(t, "dispatch", "ci.yml", "--ref", "claude/some-branch", "-R", "o/r")

	require.NoError(t, err)
	assert.Contains(t, read(), "repos/o/r/actions/workflows/ci.yml/dispatches")
	assert.Contains(t, read(), "ref=claude/some-branch")
	assert.Contains(t, out, "Dispatched ci.yml")
}

// The API answers 204 with no body, so the run has to be found afterwards.
// Saying how is the difference between a receipt and a dead end.
func TestDispatchSaysHowToFindTheRunItStarted(t *testing.T) {
	postLog(t)

	out, err := runCLI(t, "dispatch", "deploy.yml", "--ref", "master", "-R", "o/r")

	require.NoError(t, err)
	assert.Contains(t, out, "gh wait-ci runs --workflow deploy.yml")
}

// Inputs are what make a dispatch worth having: they are how a run is told
// to do the one-off thing it is being started for.
func TestDispatchForwardsEveryInput(t *testing.T) {
	read := postLog(t)

	_, err := runCLI(t, "dispatch", "ci.yml", "--ref", "master",
		"--input", "publish_site_ref=claude/x", "--input", "dry_run=true", "-R", "o/r")

	require.NoError(t, err)
	assert.Contains(t, read(), "inputs[publish_site_ref]=claude/x")
	assert.Contains(t, read(), "inputs[dry_run]=true")
}

// A malformed input must be refused rather than silently dropped: a run
// started without the input it needed looks like the feature not working.
func TestDispatchRefusesAnInputThatIsNotKeyValue(t *testing.T) {
	postLog(t)

	_, err := runCLI(t, "dispatch", "ci.yml", "--ref", "master", "--input", "nope", "-R", "o/r")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "name=value")
}
