package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logTS = "2026-06-01T02:05:01.1234567Z "

// The status column holds an emoji: three bytes wide, two terminal columns.
// Measuring bytes shifts every row after it, which is what this pins.
func TestRenderTableAlignsAnEmojiStatusColumn(t *testing.T) {
	out := renderTable(
		[]string{"", "RUN-ID", "STATE"},
		[][]string{{"✅", "1", "success"}, {"❌", "22", "failure"}},
	)
	assert.Equal(t, ""+
		"    RUN-ID  STATE\n"+
		"✅  1       success\n"+
		"❌  22      failure\n", out)
}

// A colored cell is as wide as its text: the escape occupies no column.
func TestRenderTableIgnoresANSIWidth(t *testing.T) {
	out := renderTable(nil, [][]string{
		{colorRed + "bad" + colorReset, "x"},
		{"ok", "y"},
	})
	assert.Equal(t, colorRed+"bad"+colorReset+"  x\nok   y\n", out)
}

func TestRenderLogLineCleansByDefaultAndKeepsEverythingWithRaw(t *testing.T) {
	out, ok := renderLogLine(logTS+"##[error]boom", false, false)
	require.True(t, ok)
	assert.Equal(t, colorRed+"boom"+colorReset, out)

	out, ok = renderLogLine(logTS+"##[error]boom", true, false)
	require.True(t, ok)
	assert.Equal(t, logTS+"##[error]boom", out, "raw keeps the timestamp and the marker")

	_, ok = renderLogLine(logTS+"##[endgroup]", false, false)
	assert.False(t, ok, "an endgroup marker is dropped")
}

func TestRenderLogLinePlainStripsColorFromBothSources(t *testing.T) {
	out, ok := renderLogLine(logTS+"##[error]boom", false, true)
	require.True(t, ok)
	assert.Equal(t, "boom", out)

	// A color escape the build tool itself wrote also goes.
	out, ok = renderLogLine(logTS+"\033[31mred text\033[0m", false, true)
	require.True(t, ok)
	assert.Equal(t, "red text", out)

	out, ok = renderLogLine(logTS+"\033[31mred text\033[0m", true, true)
	require.True(t, ok)
	assert.Equal(t, logTS+"red text", out, "raw still honors plain")
}

func TestStripANSILeavesOrdinaryTextAlone(t *testing.T) {
	assert.Equal(t, "plain", stripANSI("plain"))
	assert.Equal(t, "ab", stripANSI("a\033[1;32mb\033[0m"))
}

func TestStatusIconDistinguishesRunningFromFinished(t *testing.T) {
	assert.Equal(t, "🔄", statusIcon("in_progress", ""))
	assert.Equal(t, "⏳", statusIcon("queued", ""))
	assert.Equal(t, "✅", statusIcon("completed", "success"))
	assert.Equal(t, "❌", statusIcon("completed", "failure"))
	assert.Equal(t, "⛔", statusIcon("completed", "cancelled"))
	assert.Equal(t, "❔", statusIcon("completed", "something_new"))
}

func TestStateWordPrefersTheConclusionOnceComplete(t *testing.T) {
	assert.Equal(t, "in_progress", stateWord("in_progress", ""))
	assert.Equal(t, "failure", stateWord("completed", "failure"))
	assert.Equal(t, "completed", stateWord("completed", ""))
}

func mustTime(t *testing.T, s string) nullTime {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return nullTime{Time: parsed, Valid: true}
}

func TestDurationRendersMinutesAndSeconds(t *testing.T) {
	start := mustTime(t, "2026-06-01T00:00:00Z")
	assert.Equal(t, "45s", duration(start, mustTime(t, "2026-06-01T00:00:45Z")))
	assert.Equal(t, "2m05s", duration(start, mustTime(t, "2026-06-01T00:02:05Z")))
	assert.Equal(t, "-", duration(nullTime{}, mustTime(t, "2026-06-01T00:02:05Z")))
}

// A job still running has no completion timestamp, so the elapsed time is
// measured against now rather than reported as missing.
func TestDurationOfAnUnfinishedJobMeasuresAgainstNow(t *testing.T) {
	start := nullTime{Time: time.Now().Add(-90 * time.Second), Valid: true}
	assert.Equal(t, "1m30s", duration(start, nullTime{}))
}

func TestRelativeAge(t *testing.T) {
	assert.Equal(t, "-", relativeAge(nullTime{}))
	assert.Equal(t, "30s ago", relativeAge(nullTime{Time: time.Now().Add(-30 * time.Second), Valid: true}))
	assert.Equal(t, "5m ago", relativeAge(nullTime{Time: time.Now().Add(-5 * time.Minute), Valid: true}))
	assert.Equal(t, "3h ago", relativeAge(nullTime{Time: time.Now().Add(-3 * time.Hour), Valid: true}))
	assert.Equal(t, "2d ago", relativeAge(nullTime{Time: time.Now().Add(-48 * time.Hour), Valid: true}))
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "512B", humanSize(512))
	assert.Equal(t, "1.0KB", humanSize(1024))
	assert.Equal(t, "1.5MB", humanSize(1024*1024*3/2))
}

// nullTime has to survive all three shapes GitHub uses for an optional
// timestamp, because a decode error would drop the whole job object.
func TestNullTimeAcceptsNullEmptyAndRFC3339(t *testing.T) {
	var s apiStep
	require.NoError(t, json.Unmarshal([]byte(`{"started_at":null,"completed_at":""}`), &s))
	assert.False(t, s.StartedAt.Valid)
	assert.False(t, s.CompletedAt.Valid)

	require.NoError(t, json.Unmarshal([]byte(`{"started_at":"2026-06-01T00:00:00Z"}`), &s))
	assert.True(t, s.StartedAt.Valid)

	out, err := json.Marshal(nullTime{})
	require.NoError(t, err)
	assert.Equal(t, "null", string(out))
}

func TestURLValueEncodesEverythingOutsideTheUnreservedSet(t *testing.T) {
	assert.Equal(t, "claude%2Fmy-branch", urlValue("claude/my-branch"))
	assert.Equal(t, "ci.yml", urlValue("ci.yml"))
	assert.Equal(t, "a%20b", urlValue("a b"))
}

func TestRunFilterLimitIsClamped(t *testing.T) {
	assert.Equal(t, 20, runFilter{}.limitOrDefault())
	assert.Equal(t, 100, runFilter{Limit: 500}.limitOrDefault())
	assert.Equal(t, 7, runFilter{Limit: 7}.limitOrDefault())
}

func TestShortSHA(t *testing.T) {
	assert.Equal(t, "abc1234", shortSHA("abc1234def5678"))
	assert.Equal(t, "abc", shortSHA("abc"))
}
