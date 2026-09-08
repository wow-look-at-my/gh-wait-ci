package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobLog assembles a per-job log the way GitHub serves one: every step's chunk
// opens with a BOM, and a skipped step contributes nothing at all.
func jobLog(chunks ...[]string) []string {
	var lines []string
	for _, c := range chunks {
		for i, l := range c {
			if i == 0 {
				l = stepBOM + l
			}
			lines = append(lines, l)
		}
	}
	return lines
}

func buildJob() apiJob {
	return apiJob{
		ID: 7, Name: "build", Conclusion: "failure",
		Steps: []apiStep{
			{Number: 1, Name: "Set up job", Conclusion: "success"},
			{Number: 2, Name: "Assemble plugin-bundle", Conclusion: "failure"},
			{Number: 3, Name: "Install dats", Conclusion: "skipped"},
			{Number: 4, Name: "Complete job", Conclusion: "success"},
		},
	}
}

func TestSplitJobLogPairsBOMChunksWithTheStepsThatRan(t *testing.T) {
	lines := jobLog(
		[]string{"Current runner version", "Runner Image"},
		[]string{"##[group]Run node assemble.ts", "fatal: not found", "##[error]exit 1"},
		[]string{"Cleaning up orphan processes"},
	)

	sections := splitJobLog(buildJob(), lines)
	require.Len(t, sections, 3, "one section per step that produced output")

	assert.Equal(t, 1, sections[0].StepNumber)
	assert.Equal(t, "Set up job", sections[0].StepName)
	assert.Equal(t, "build / Assemble plugin-bundle", sections[1].Label())
	assert.Equal(t, "failure", sections[1].Conclusion)

	// The skipped step writes nothing, so it must not consume a chunk: the
	// third chunk belongs to step 4, not step 3.
	assert.Equal(t, 4, sections[2].StepNumber)
	assert.Equal(t, "Complete job", sections[2].StepName)
}

func TestSplitJobLogStripsTheBOMFromTheLineItOpens(t *testing.T) {
	sections := splitJobLog(buildJob(), jobLog(
		[]string{"first"}, []string{"second"}, []string{"third"},
	))
	require.Len(t, sections, 3)
	for _, s := range sections {
		assert.NotContains(t, s.Lines[0], stepBOM, "the boundary marker is not log text")
	}
	assert.Equal(t, []string{"second"}, sections[1].Lines)
}

// Failed() is what --failed filters on, so the conclusion has to arrive from
// the API rather than from a guess about the text.
func TestSplitJobLogGivesFailedOnlyItsFilter(t *testing.T) {
	sections := splitJobLog(buildJob(), jobLog(
		[]string{"a"}, []string{"b"}, []string{"c"},
	))
	require.Len(t, sections, 3)

	var failed []string
	for _, s := range sections {
		if s.Failed() {
			failed = append(failed, s.StepName)
		}
	}
	assert.Equal(t, []string{"Assemble plugin-bundle"}, failed)
}

// The negative controls. Each one must return nil so the caller keeps the whole
// job: a wrong step name on the log somebody is reading to find a failure is
// worse than no split.

func TestSplitJobLogDeclinesALogWithNoBoundaries(t *testing.T) {
	assert.Nil(t, splitJobLog(buildJob(), []string{"no marker here", "nor here"}))
}

func TestSplitJobLogDeclinesWhenTheChunksOutnumberTheSteps(t *testing.T) {
	lines := jobLog(
		[]string{"a"}, []string{"b"}, []string{"c"}, []string{"d"},
	)
	assert.Nil(t, splitJobLog(buildJob(), lines), "4 chunks against 3 steps that ran")
}

func TestSplitJobLogDeclinesWhenTheStepsOutnumberTheChunks(t *testing.T) {
	assert.Nil(t, splitJobLog(buildJob(), jobLog([]string{"a"}, []string{"b"})))
}

func TestSplitJobLogDeclinesAJobTheAPIReportedNoStepsFor(t *testing.T) {
	job := apiJob{ID: 7, Name: "build"}
	assert.Nil(t, splitJobLog(job, jobLog([]string{"a"}, []string{"b"})))
}
