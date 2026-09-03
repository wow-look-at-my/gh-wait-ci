package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleSections() []logSection {
	return []logSection{
		{
			JobID: 111, JobName: "build", StepNumber: 2, StepName: "Compile", Conclusion: "success",
			Lines: []string{
				logTS + "starting compile",
				logTS + "warning: unused variable",
				logTS + "compile done",
			},
		},
		{
			JobID: 222, JobName: "test", StepNumber: 3, StepName: "Run tests", Conclusion: "failure",
			Lines: []string{
				logTS + "running tests",
				logTS + "##[error]FAIL: TestThing",
				logTS + "1 failed",
			},
		},
	}
}

// A pattern is written against what a reader sees, so matching runs on the
// cleaned line: no RFC3339 prefix and no ##[...] marker to work around.
func TestSearchSectionsMatchesTheCleanedText(t *testing.T) {
	re, err := buildPattern("^FAIL", false, false)
	require.NoError(t, err)

	matches := searchSections(sampleSections(), re, 0, 0, false)
	require.Len(t, matches, 1)
	assert.Equal(t, "FAIL: TestThing", matches[0].Text)
	assert.Equal(t, 2, matches[0].Line)
	assert.Equal(t, int64(222), matches[0].JobID)
	assert.Equal(t, "test / Run tests", matches[0].key())
}

func TestSearchSectionsContextLinesAreClampedToTheSection(t *testing.T) {
	re, err := buildPattern("unused", false, false)
	require.NoError(t, err)

	matches := searchSections(sampleSections(), re, 5, 5, false)
	require.Len(t, matches, 1)
	assert.Equal(t, []string{"starting compile"}, matches[0].Before)
	assert.Equal(t, []string{"compile done"}, matches[0].After)
}

func TestSearchSectionsInvertKeepsTheNonMatchingLines(t *testing.T) {
	re, err := buildPattern("compile", false, false)
	require.NoError(t, err)

	matches := searchSections(sampleSections()[:1], re, 0, 0, true)
	require.Len(t, matches, 1)
	assert.Equal(t, "warning: unused variable", matches[0].Text)
}

func TestBuildPatternFixedAndIgnoreCase(t *testing.T) {
	re, err := buildPattern("a.c", true, false)
	require.NoError(t, err)
	assert.True(t, re.MatchString("a.c"))
	assert.False(t, re.MatchString("abc"), "--fixed must not treat . as a wildcard")

	re, err = buildPattern("FAIL", false, true)
	require.NoError(t, err)
	assert.True(t, re.MatchString("fail"))

	_, err = buildPattern("a(", false, false)
	require.Error(t, err)
}

func TestSearchSectionsFindsNothingWhenThePatternDoesNotOccur(t *testing.T) {
	re, err := buildPattern("no-such-text", false, false)
	require.NoError(t, err)
	assert.Empty(t, searchSections(sampleSections(), re, 0, 0, false))
}

func TestCountBySectionKeepsFirstSeenOrder(t *testing.T) {
	re, err := buildPattern("e", false, false)
	require.NoError(t, err)

	order, counts := countBySection(searchSections(sampleSections(), re, 0, 0, false))
	require.Equal(t, []string{"build / Compile", "test / Run tests"}, order)
	assert.Positive(t, counts["build / Compile"])
	assert.Positive(t, counts["test / Run tests"])
}

func TestGrepMatchKeyOmitsTheStepForAWholeJobSection(t *testing.T) {
	assert.Equal(t, "build", grepMatch{Job: "build"}.key())
	assert.Equal(t, "build / Compile", grepMatch{Job: "build", Step: "Compile"}.key())
}

func TestMinMaxHelpers(t *testing.T) {
	assert.Equal(t, 3, maxInt(3, 1))
	assert.Equal(t, 3, maxInt(1, 3))
	assert.Equal(t, 1, minInt(3, 1))
	assert.Equal(t, 1, minInt(1, 3))
}
