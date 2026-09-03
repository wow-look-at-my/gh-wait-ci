package main

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildZip assembles an in-memory archive shaped like the one GitHub serves for
// a whole run: a root file per job, and a directory of step files per job.
func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestSplitLinesDoesNotInventATrailingLine(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, splitLines("a\nb\n"))
	assert.Equal(t, []string{"a", "b"}, splitLines("a\nb"))
	assert.Nil(t, splitLines(""))
	assert.Nil(t, splitLines("\n"))
}

func TestReadRunLogZipKeepsOnlyTextEntries(t *testing.T) {
	data := buildZip(t, map[string]string{
		"build/1_Set up job.txt": "hello\n",
		"build/notes.md":         "ignored",
	})
	files, err := readRunLogZip(data)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "build/1_Set up job.txt", files[0].path)
	assert.Equal(t, "hello\n", files[0].body)
}

func TestReadRunLogZipRejectsNonArchiveBytes(t *testing.T) {
	_, err := readRunLogZip([]byte("this is not a zip"))
	require.Error(t, err)
}

func TestSectionsFromZipPrefersStepFilesOverTheWholeJobFile(t *testing.T) {
	jobs := []apiJob{{
		ID: 111, Name: "build", Conclusion: "failure",
		Steps: []apiStep{
			{Number: 1, Name: "Set up job", Conclusion: "success"},
			{Number: 2, Name: "Run tests", Conclusion: "failure"},
		},
	}}
	data := buildZip(t, map[string]string{
		"0_build.txt":            "whole job log\n",
		"build/1_Set up job.txt": "setting up\n",
		"build/2_Run tests.txt":  "boom\n",
	})
	files, err := readRunLogZip(data)
	require.NoError(t, err)

	sections := sectionsFromZip(files, jobs)
	require.Len(t, sections, 2, "the whole-job file must not be added alongside the step files")
	assert.Equal(t, 1, sections[0].StepNumber)
	assert.Equal(t, "Set up job", sections[0].StepName)
	assert.Equal(t, 2, sections[1].StepNumber)
	assert.Equal(t, []string{"boom"}, sections[1].Lines)
	assert.Equal(t, int64(111), sections[1].JobID)
	assert.True(t, sections[1].Failed())
	assert.False(t, sections[0].Failed())
}

func TestSectionsFromZipFallsBackToTheWholeJobFile(t *testing.T) {
	jobs := []apiJob{{ID: 222, Name: "lint", Conclusion: "success"}}
	data := buildZip(t, map[string]string{"0_lint.txt": "clean\n"})
	files, err := readRunLogZip(data)
	require.NoError(t, err)

	sections := sectionsFromZip(files, jobs)
	require.Len(t, sections, 1)
	assert.Equal(t, 0, sections[0].StepNumber)
	assert.Equal(t, "lint", sections[0].Label())
	assert.Equal(t, []string{"clean"}, sections[0].Lines)
}

// A matrix job's name carries a "/", so splitting the archive path on the last
// separator would attribute its step files to a job that does not exist.
func TestSectionsFromZipMatchesAJobNameContainingASlash(t *testing.T) {
	jobs := []apiJob{{
		ID: 333, Name: "test / ubuntu",
		Steps: []apiStep{{Number: 3, Name: "Run", Conclusion: "success"}},
	}}
	data := buildZip(t, map[string]string{"test / ubuntu/3_Run.txt": "ok\n"})
	files, err := readRunLogZip(data)
	require.NoError(t, err)

	sections := sectionsFromZip(files, jobs)
	require.Len(t, sections, 1)
	assert.Equal(t, "test / ubuntu", sections[0].JobName)
	assert.Equal(t, "test / ubuntu / Run", sections[0].Label())
}

func TestSectionsFromZipKeepsRunOrderAndStepOrder(t *testing.T) {
	jobs := []apiJob{
		{ID: 1, Name: "build", Steps: []apiStep{{Number: 1, Name: "a"}, {Number: 2, Name: "b"}}},
		{ID: 2, Name: "test", Steps: []apiStep{{Number: 1, Name: "c"}}},
	}
	data := buildZip(t, map[string]string{
		"test/1_c.txt":  "c\n",
		"build/2_b.txt": "b\n",
		"build/1_a.txt": "a\n",
	})
	files, err := readRunLogZip(data)
	require.NoError(t, err)

	sections := sectionsFromZip(files, jobs)
	require.Len(t, sections, 3)
	assert.Equal(t, []string{"build / a", "build / b", "test / c"},
		[]string{sections[0].Label(), sections[1].Label(), sections[2].Label()})
}

func TestMatchesJobAcceptsAnIDOrACaseInsensitiveSubstring(t *testing.T) {
	job := apiJob{ID: 4242, Name: "Build And Test"}
	assert.True(t, matchesJob(job, ""))
	assert.True(t, matchesJob(job, "4242"))
	assert.True(t, matchesJob(job, "and test"))
	assert.False(t, matchesJob(job, "4243"))
	assert.False(t, matchesJob(job, "lint"))
}

func TestMatchesStepAcceptsANumberOrACaseInsensitiveSubstring(t *testing.T) {
	s := logSection{StepNumber: 7, StepName: "Run go-toolchain"}
	assert.True(t, matchesStep(s, ""))
	assert.True(t, matchesStep(s, "7"))
	assert.True(t, matchesStep(s, "TOOLCHAIN"))
	assert.False(t, matchesStep(s, "8"))
	assert.False(t, matchesStep(s, "deploy"))
}

func TestJobFailedTreatsAnUnfinishedJobAsNotFailed(t *testing.T) {
	assert.False(t, jobFailed(apiJob{Status: "in_progress"}))
	assert.False(t, jobFailed(apiJob{Status: "completed", Conclusion: "success"}))
	assert.False(t, jobFailed(apiJob{Status: "completed", Conclusion: "skipped"}))
	assert.True(t, jobFailed(apiJob{Status: "completed", Conclusion: "failure"}))
	assert.True(t, jobFailed(apiJob{Status: "completed", Conclusion: "timed_out"}))
	assert.True(t, jobFailed(apiJob{Status: "completed", Conclusion: "cancelled"}))
}
