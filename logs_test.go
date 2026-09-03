package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCleanLogLine(t *testing.T) {
	const ts = "2026-06-01T02:05:01.1234567Z "

	cases := []struct {
		name     string
		in       string
		wantText string
		wantOK   bool
	}{
		{"plain line strips timestamp", ts + "hello world", "hello world", true},
		{"leading BOM is stripped before timestamp", "\ufeff" + ts + "first line", "first line", true},
		{"group marker normalized", ts + "##[group]Run the build", "‣ Run the build", true},
		{"endgroup dropped", ts + "##[endgroup]", "", false},
		{"error marker colorized", ts + "##[error]boom", colorRed + "boom" + colorReset, true},
		{"warning marker colorized", ts + "##[warning]careful", colorYellow + "careful" + colorReset, true},
		{"command marker colorized", ts + "##[command]echo hi", colorBlue + "echo hi" + colorReset, true},
		{"no timestamp passes through", "no timestamp here", "no timestamp here", true},
		{"trailing carriage return trimmed", ts + "progress\r", "progress", true},
		{"empty after timestamp", ts, "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotText, gotOK := cleanLogLine(c.in)
			assert.False(t, gotText != c.wantText || gotOK != c.wantOK)

		})
	}
}

func TestEmitDeltaAppendOnly(t *testing.T) {
	st := &jobLogState{label: "job"}

	// First poll: two complete lines plus a partial third line. Only the two
	// complete lines should be consumed; the partial line waits.
	emitDelta(st, "2026-06-01T00:00:00.0000000Z a\n2026-06-01T00:00:00.0000000Z b\npart", false, false)
	require.NotEqual(t, 0, st.printed)

	afterFirst := st.printed

	// Second poll: the partial line is now complete. printed must advance.
	emitDelta(st, "2026-06-01T00:00:00.0000000Z a\n2026-06-01T00:00:00.0000000Z b\npartial done\n", false, false)
	require.Greater(t, st.printed, afterFirst)

	// A shorter raw (should never happen) must not panic or rewind.
	before := st.printed
	emitDelta(st, "short", false, false)
	require.Equal(t, before, st.printed)

}
