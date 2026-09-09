package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wow-look-at-my/api-cli/fields"
)

// ansiRe matches an ANSI SGR escape, so --plain can strip both the colors this
// tool adds and the ones a build tool wrote into the log itself.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// renderLogLine prepares one raw log line for display. raw keeps the timestamp
// and the ##[...] workflow markers exactly as GitHub stored them; plain removes
// every color escape. The bool is false when the line should be dropped.
func renderLogLine(line string, raw, plain bool) (string, bool) {
	if raw {
		line = strings.TrimRight(line, "\r")
		if plain {
			line = stripANSI(line)
		}
		return line, true
	}
	out, ok := cleanLogLine(line)
	if !ok {
		return "", false
	}
	if plain {
		out = stripANSI(out)
	}
	return out, true
}

// statusIcon renders a status/conclusion pair as one glyph.
func statusIcon(status, conclusion string) string {
	if status != "completed" {
		switch status {
		case "in_progress":
			return "🔄"
		case "queued", "waiting", "pending", "requested":
			return "⏳"
		}
		return "⏳"
	}
	switch conclusion {
	case "success":
		return "✅"
	case "failure":
		return "❌"
	case "cancelled":
		return "⛔"
	case "skipped":
		return "⏭️ "
	case "timed_out":
		return "⏱️ "
	case "action_required":
		return "⚠️ "
	case "neutral":
		return "➖"
	}
	return "❔"
}

// stateWord renders a status/conclusion pair as one word for a table column.
func stateWord(status, conclusion string) string {
	if status != "completed" {
		return status
	}
	if conclusion == "" {
		return "completed"
	}
	return conclusion
}

// duration renders the elapsed time between two optional timestamps.
func duration(start, end nullTime) string {
	if !start.Valid {
		return "-"
	}
	stop := time.Now()
	if end.Valid {
		stop = end.Time
	}
	d := stop.Sub(start.Time).Round(time.Second)
	if d < 0 {
		return "-"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// relativeAge renders how long ago a timestamp was.
func relativeAge(t nullTime) string {
	if !t.Valid {
		return "-"
	}
	d := time.Since(t.Time).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// humanSize renders a byte count for an artifact listing.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}

// printTable prints rows in aligned columns. Every row must have as many cells
// as the header. The status column holds an emoji, which is two terminal
// columns wide and three bytes long, so the aligner measures display width
// rather than length.
func printTable(header []string, rows [][]string) {
	fmt.Print(renderTable(header, rows))
}

// renderTable is printTable's output, returned rather than printed, so a test
// reads it without taking over stdout.
func renderTable(header []string, rows [][]string) string {
	lines := make([]string, 0, len(rows)+1)
	if len(header) > 0 {
		lines = append(lines, strings.Join(header, "\t"))
	}
	for _, r := range rows {
		lines = append(lines, strings.Join(r, "\t"))
	}
	return fields.AlignColumns(lines, 2)
}
