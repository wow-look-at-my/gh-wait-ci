package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRateLimit puts a `gh` on PATH answering `api rate_limit` with body, so
// the hint is exercised the way it runs: by shelling out.
func stubRateLimit(t *testing.T, body string, exit int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' '%s'\nexit %d\n", body, exit)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func rateLimitBody(t *testing.T, used, remaining int, reset int64) string {
	t.Helper()
	var st rateLimitState
	st.Rate.Limit = 5000
	st.Rate.Used = used
	st.Rate.Remaining = remaining
	st.Rate.Reset = reset
	b, err := json.Marshal(st)
	require.NoError(t, err)
	return string(b)
}

// A failure that is not a rate limit must stay exactly as it was: the hint
// costs a subprocess, and every other error already says what it is.
func TestNoHintForAnUnrelatedFailure(t *testing.T) {
	assert.Empty(t, rateLimitHint("HTTP 404: Not Found"))
	assert.Empty(t, rateLimitHint("could not resolve host"))
}

// The case this exists for. GitHub says "rate limit exceeded" while the
// hourly budget is untouched, and the count is the only thing that says so.
//
// `Unix()` drops the sub-second part of now, so the wait is a whole number of
// seconds short of the offset by however far into the second the test started.
// Rounded, that is one of two values. The pattern pins every other character.
func TestHeadroomLeftIsReportedAsACount(t *testing.T) {
	stubRateLimit(t, rateLimitBody(t, 0, 5000, time.Now().Add(42*time.Minute).Unix()), 0)

	got := rateLimitHint("gh: API rate limit exceeded for user ID 6569500 (HTTP 403)")

	assert.Regexp(t, `^gh-wait-ci: core 0/5000 used, 5000 left, resets in 4(1m59s|2m0s)$`, got)
}

// The other limit, where the count really does mean waiting for the reset.
func TestExhaustedBudgetIsReportedAsACount(t *testing.T) {
	stubRateLimit(t, rateLimitBody(t, 5000, 0, time.Now().Add(17*time.Minute).Unix()), 0)

	got := rateLimitHint("API rate limit exceeded")

	assert.Regexp(t, `^gh-wait-ci: core 5000/5000 used, 0 left, resets in 1(6m59s|7m0s)$`, got)
}

// The budget read can fail too. Saying so beats printing a made-up count.
func TestUnreadableBudgetSaysSoRatherThanGuessing(t *testing.T) {
	stubRateLimit(t, "", 1)
	assert.Equal(t, "gh-wait-ci: core budget unreadable", rateLimitHint("API rate limit exceeded"))

	stubRateLimit(t, "not json at all", 0)
	assert.Equal(t, "gh-wait-ci: core budget unreadable", rateLimitHint("API rate limit exceeded"))
}

// A reset already past must not render as a negative wait.
func TestAPastResetReadsAsZero(t *testing.T) {
	assert.Equal(t, "0s", resetIn(time.Now().Add(-time.Minute).Unix()))
	assert.Equal(t, "?", resetIn(0))
}
