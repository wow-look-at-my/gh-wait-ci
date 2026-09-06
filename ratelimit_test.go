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
// hourly budget is untouched, which is the SECONDARY limit. Reading that
// message alone leads to waiting an hour for a refill that already happened.
func TestSecondaryLimitIsNamedWhenTheBudgetIsIntact(t *testing.T) {
	reset := time.Now().Add(42 * time.Minute).Unix()
	stubRateLimit(t, rateLimitBody(t, 0, 5000, reset), 0)

	got := rateLimitHint("gh: API rate limit exceeded for user ID 6569500 (HTTP 403)")

	assert.Contains(t, got, "SECONDARY limit")
	assert.Contains(t, got, "0/5000 used")
	assert.Contains(t, got, "5000 remaining")
	assert.NotContains(t, got, "hourly budget IS spent")
}

// The other limit, which really does mean waiting for the stated refill.
func TestExhaustedHourlyBudgetIsNamedWithItsRefill(t *testing.T) {
	reset := time.Now().Add(17 * time.Minute).Unix()
	stubRateLimit(t, rateLimitBody(t, 5000, 0, reset), 0)

	got := rateLimitHint("API rate limit exceeded")

	assert.Contains(t, got, "hourly budget IS spent")
	assert.Contains(t, got, "5000/5000 used")
	assert.Contains(t, got, "in 16m")
	assert.NotContains(t, got, "SECONDARY")
}

// The budget read can fail too. Saying so beats a silent omission, which
// would read as though the limit had been classified.
func TestUnreadableBudgetSaysSoRatherThanGuessing(t *testing.T) {
	stubRateLimit(t, "", 1)
	assert.Contains(t, rateLimitHint("API rate limit exceeded"), "could not read the rate-limit budget")

	stubRateLimit(t, "not json at all", 0)
	assert.Contains(t, rateLimitHint("API rate limit exceeded"), "did not parse")
}

// A reset already in the past must not render as a negative wait.
func TestAPastResetReadsAsDue(t *testing.T) {
	assert.Contains(t, humanReset(time.Now().Add(-time.Minute).Unix()), "already due")
	assert.Contains(t, humanReset(0), "unreported")
}
