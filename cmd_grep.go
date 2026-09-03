package main

import (
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-containers/set"
)

// grepMatch is one matching log line, with enough context to find it again.
type grepMatch struct {
	Job     string   `json:"job"`
	JobID   int64    `json:"job_id"`
	Step    string   `json:"step,omitempty"`
	StepNum int      `json:"step_number,omitempty"`
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
}

// key names the section a match came from, and is what groups the output.
func (m grepMatch) key() string {
	if m.Step == "" {
		return m.Job
	}
	return m.Job + " / " + m.Step
}

// buildPattern turns the user's pattern and its modifier flags into a regexp.
func buildPattern(pattern string, fixed, ignoreCase bool) (*regexp.Regexp, error) {
	if fixed {
		pattern = regexp.QuoteMeta(pattern)
	}
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	return re, nil
}

// searchSections finds every line of every section that the pattern selects.
//
// Matching runs against the CLEANED text, so a pattern never has to account for
// the RFC3339 timestamp GitHub prefixes onto every raw log line, nor for the
// ##[...] markers it wraps groups and errors in.
func searchSections(sections []logSection, re *regexp.Regexp, before, after int, invert bool) []grepMatch {
	var matches []grepMatch
	for _, s := range sections {
		clean := make([]string, len(s.Lines))
		for i, line := range s.Lines {
			if out, ok := renderLogLine(line, false, true); ok {
				clean[i] = out
			}
		}
		for i, text := range clean {
			if re.MatchString(text) == invert {
				continue
			}
			m := grepMatch{Job: s.JobName, JobID: s.JobID, Step: s.StepName, StepNum: s.StepNumber, Line: i + 1, Text: text}
			for j := maxInt(0, i-before); j < i; j++ {
				m.Before = append(m.Before, clean[j])
			}
			for j := i + 1; j <= minInt(len(clean)-1, i+after); j++ {
				m.After = append(m.After, clean[j])
			}
			matches = append(matches, m)
		}
	}
	return matches
}

// countBySection tallies matches per section, keeping first-seen order.
func countBySection(matches []grepMatch) ([]string, map[string]int) {
	counts := map[string]int{}
	var order []string
	for _, m := range matches {
		k := m.key()
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	return order, counts
}

func newGrepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grep <pattern> [run-id]",
		Short: "Search a run's logs for a pattern",
		Long: "Search every log line of a run for a pattern, and print each match with\n" +
			"the job and step it came from. The pattern is a Go regular expression\n" +
			"unless --fixed is given. Exits non-zero when nothing matches.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sel := selector{}
			if len(args) > 1 {
				sel.RunID = args[1]
			}
			sel.SHA, _ = cmd.Flags().GetString("sha")
			sel.Workflow, _ = cmd.Flags().GetString("workflow")
			sel.Attempt, _ = cmd.Flags().GetInt("attempt")

			fixed, _ := cmd.Flags().GetBool("fixed")
			ignoreCase, _ := cmd.Flags().GetBool("ignore-case")
			re, err := buildPattern(args[0], fixed, ignoreCase)
			if err != nil {
				return err
			}

			tgt, err := resolveTarget(sel)
			if err != nil {
				return err
			}
			opt := collectOptions{}
			opt.Job, _ = cmd.Flags().GetString("job")
			opt.Step, _ = cmd.Flags().GetString("step")
			opt.FailedOnly, _ = cmd.Flags().GetBool("failed")

			sections, _, err := collectSections(tgt.Repo, tgt.Run.ID, tgt.Jobs, opt)
			if err != nil {
				return err
			}

			before, _ := cmd.Flags().GetInt("before-context")
			after, _ := cmd.Flags().GetInt("after-context")
			if ctx, _ := cmd.Flags().GetInt("context"); ctx > 0 {
				before, after = ctx, ctx
			}
			invert, _ := cmd.Flags().GetBool("invert-match")
			plain, _ := cmd.Flags().GetBool("plain")

			matches := searchSections(sections, re, before, after, invert)

			asJSON, _ := cmd.Flags().GetBool("json")
			countOnly, _ := cmd.Flags().GetBool("count")
			listOnly, _ := cmd.Flags().GetBool("sections-with-matches")

			switch {
			case asJSON:
				if matches == nil {
					matches = []grepMatch{}
				}
				if err := emitJSON(matches); err != nil {
					return err
				}
			case countOnly:
				order, counts := countBySection(matches)
				for _, k := range order {
					fmt.Printf("%s: %d\n", k, counts[k])
				}
			case listOnly:
				seen := set.New[string]()
				for _, m := range matches {
					if !seen.Contains(m.key()) {
						seen.Add(m.key())
						fmt.Println(m.key())
					}
				}
			default:
				printMatches(matches, re, plain)
			}

			if len(matches) == 0 {
				return errNoMatches
			}
			return nil
		},
	}
	addSelectorFlags(cmd)
	cmd.Flags().StringP("job", "j", "", "Only this job (job ID, or part of its name)")
	cmd.Flags().String("step", "", "Only this step (step number, or part of its name)")
	cmd.Flags().Bool("failed", false, "Only search jobs and steps that did not succeed")
	cmd.Flags().BoolP("ignore-case", "i", false, "Case-insensitive match")
	cmd.Flags().BoolP("fixed", "F", false, "Treat the pattern as a literal string")
	cmd.Flags().BoolP("invert-match", "v", false, "Print the lines that do NOT match")
	cmd.Flags().IntP("context", "C", 0, "Lines of context on both sides")
	cmd.Flags().IntP("before-context", "B", 0, "Lines of context before each match")
	cmd.Flags().IntP("after-context", "A", 0, "Lines of context after each match")
	cmd.Flags().BoolP("count", "c", false, "Print only the match count per section")
	cmd.Flags().BoolP("sections-with-matches", "l", false, "Print only the sections that contain a match")
	cmd.Flags().Bool("plain", false, "Strip every color escape")
	cmd.Flags().Bool("json", false, "Emit the matches as JSON")
	return cmd
}

// printMatches renders matches grouped by section, in grep's line-number style.
func printMatches(matches []grepMatch, re *regexp.Regexp, plain bool) {
	lastKey := ""
	for _, m := range matches {
		if m.key() != lastKey {
			if plain {
				fmt.Printf("\n══ %s\n", m.key())
			} else {
				fmt.Printf("\n%s══ %s%s\n", colorBlue, m.key(), colorReset)
			}
			lastKey = m.key()
		}
		for i, b := range m.Before {
			fmt.Printf("%6d- %s\n", m.Line-len(m.Before)+i, b)
		}
		text := m.Text
		if !plain {
			text = re.ReplaceAllStringFunc(text, func(s string) string { return colorRed + s + colorReset })
		}
		fmt.Printf("%6d: %s\n", m.Line, text)
		for i, a := range m.After {
			fmt.Printf("%6d- %s\n", m.Line+i+1, a)
		}
	}
}

// errNoMatches makes `grep` exit non-zero on no match, the way grep itself does,
// so a script can branch on it.
var errNoMatches = &silentError{}

// silentError carries a non-zero exit without printing an error line.
type silentError struct{}

func (e *silentError) Error() string { return "no matches" }
