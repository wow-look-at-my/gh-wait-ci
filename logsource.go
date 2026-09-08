package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// logSection is one addressable piece of a run's logs: either a whole job, or
// one step of a job. Steps are what GitHub renders as the collapsible groups on
// the run page, and they are the finest granularity the API exposes.
type logSection struct {
	JobID      int64
	JobName    string
	StepNumber int // 0 when the section is a whole job
	StepName   string
	Conclusion string
	Lines      []string
}

// Label renders the section as "job / step", or just the job when the section
// covers the whole job.
func (s logSection) Label() string {
	if s.StepNumber == 0 {
		return s.JobName
	}
	return fmt.Sprintf("%s / %s", s.JobName, s.StepName)
}

// Failed reports whether this section's conclusion is one a reader is hunting
// for. An empty conclusion (still running) is not a failure.
func (s logSection) Failed() bool {
	switch s.Conclusion {
	case "", "success", "skipped", "neutral":
		return false
	}
	return true
}

// zipEntryRe matches the "<number>_<name>.txt" basename GitHub gives every log
// file inside the run archive.
var zipEntryRe = regexp.MustCompile(`^(\d+)_(.+)\.txt$`)

// fetchRunLogZip downloads the whole-run log archive. GitHub only produces it
// once the run has finished; while the run is in progress the endpoint answers
// 404, and the caller falls back to per-job logs.
func fetchRunLogZip(repo string, runID int64) ([]byte, error) {
	return ghAPIBytes(fmt.Sprintf("repos/%s/actions/runs/%d/logs", repo, runID))
}

// fetchJobLogText downloads the plain-text log of a single job. This works as
// soon as the job COMPLETES, so it is what makes per-job output available while
// the rest of the run is still going.
func fetchJobLogText(repo string, jobID int64) (string, error) {
	out, err := ghAPIBytes(fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// splitLines splits a log body into lines without inventing a trailing empty one.
func splitLines(body string) []string {
	body = strings.TrimSuffix(body, "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// stepBOM opens every step's chunk inside a per-job log. It is the only step
// boundary those logs carry. They hold no ##[start-action] pairs: those live in
// the run archive, which GitHub publishes only once the whole run finishes.
// Built from its code point: Go rejects a literal BOM in a source file.
const bomRune rune = 0xFEFF

var stepBOM = string(bomRune)

// splitJobLog cuts one job's log into a section per step.
//
// The BOM gives the boundaries. The names and outcomes come from the API,
// because the text names only the steps that open with a ##[group]Run header
// and never states an outcome at all. A skipped step writes nothing, so the
// chunks pair with the steps that ran, in order.
//
// A pairing that does not line up returns nil and the caller keeps the whole
// job. A wrong step name on the log somebody is reading to find a failure is
// worse than no split.
func splitJobLog(job apiJob, lines []string) []logSection {
	var starts []int
	for i, l := range lines {
		if strings.HasPrefix(l, stepBOM) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return nil
	}

	var ran []apiStep
	for _, s := range job.Steps {
		if s.Conclusion == "skipped" {
			continue
		}
		ran = append(ran, s)
	}
	if len(ran) != len(starts) {
		return nil
	}

	sections := make([]logSection, 0, len(starts))
	for i, start := range starts {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		chunk := make([]string, end-start)
		copy(chunk, lines[start:end])
		chunk[0] = strings.TrimPrefix(chunk[0], stepBOM)
		sections = append(sections, logSection{
			JobID:      job.ID,
			JobName:    job.Name,
			StepNumber: ran[i].Number,
			StepName:   ran[i].Name,
			Conclusion: ran[i].Conclusion,
			Lines:      chunk,
		})
	}
	return sections
}

// zipFile is one parsed entry of the run archive.
type zipFile struct {
	path string
	body string
}

// readRunLogZip unpacks the archive into its entries, in archive order.
func readRunLogZip(data []byte) ([]zipFile, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("could not read the run log archive: %w", err)
	}
	var out []zipFile
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".txt") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("could not open %s in the run log archive: %w", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("could not read %s in the run log archive: %w", f.Name, err)
		}
		out = append(out, zipFile{path: f.Name, body: string(body)})
	}
	return out, nil
}

// sectionsFromZip turns the archive entries into per-step sections, matched back
// to the jobs they belong to.
//
// The archive holds a directory per job, containing one file per step, plus a
// root-level file per job holding that job's whole log. The step files are the
// finer view, so a job contributes its root file only when it has no step files.
// A job name can itself contain a "/", so the job is found by longest-prefix
// match against the real job names rather than by splitting the path.
func sectionsFromZip(files []zipFile, jobs []apiJob) []logSection {
	byName := map[string]apiJob{}
	names := make([]string, 0, len(jobs))
	for _, j := range jobs {
		byName[j.Name] = j
		names = append(names, j.Name)
	}
	// Longest first, so "build (linux)" wins over a hypothetical "build".
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	stepsFor := func(job apiJob, number int) apiStep {
		for _, s := range job.Steps {
			if s.Number == number {
				return s
			}
		}
		return apiStep{Number: number}
	}

	var stepSections []logSection
	jobLevel := map[string]logSection{}
	sawSteps := set.New[string]()

	for _, f := range files {
		base := f.path
		dir := ""
		if i := strings.LastIndexByte(f.path, '/'); i >= 0 {
			dir, base = f.path[:i], f.path[i+1:]
		}
		m := zipEntryRe.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		number, _ := strconv.Atoi(m[1])
		name := m[2]

		if dir == "" {
			// A root-level file is the whole job log; its name IS the job name.
			job, ok := byName[name]
			if !ok {
				continue
			}
			jobLevel[name] = logSection{
				JobID: job.ID, JobName: job.Name, Conclusion: job.Conclusion,
				Lines: splitLines(f.body),
			}
			continue
		}

		jobName := ""
		for _, n := range names {
			if dir == n || strings.HasPrefix(dir, n+"/") {
				jobName = n
				break
			}
		}
		if jobName == "" {
			continue
		}
		job := byName[jobName]
		step := stepsFor(job, number)
		stepName := step.Name
		if stepName == "" {
			stepName = name
		}
		sawSteps.Add(jobName)
		stepSections = append(stepSections, logSection{
			JobID: job.ID, JobName: job.Name, StepNumber: number, StepName: stepName,
			Conclusion: step.Conclusion, Lines: splitLines(f.body),
		})
	}

	// Keep the run's own job order, and each job's step order within it.
	order := map[string]int{}
	for i, j := range jobs {
		order[j.Name] = i
	}
	sort.SliceStable(stepSections, func(i, j int) bool {
		if order[stepSections[i].JobName] != order[stepSections[j].JobName] {
			return order[stepSections[i].JobName] < order[stepSections[j].JobName]
		}
		return stepSections[i].StepNumber < stepSections[j].StepNumber
	})

	var out []logSection
	for _, j := range jobs {
		if sawSteps.Contains(j.Name) {
			for _, s := range stepSections {
				if s.JobName == j.Name {
					out = append(out, s)
				}
			}
			continue
		}
		if s, ok := jobLevel[j.Name]; ok {
			out = append(out, s)
		}
	}
	return out
}

// collectOptions narrows which sections are gathered.
type collectOptions struct {
	Job        string // job name substring, or a numeric job ID
	Step       string // step name substring, or a step number
	FailedOnly bool
	Attempt    int
}

// matchesJob reports whether a job satisfies the --job selector: a numeric job
// ID, or a case-insensitive substring of the job name.
func matchesJob(job apiJob, sel string) bool {
	if sel == "" {
		return true
	}
	if id, err := strconv.ParseInt(sel, 10, 64); err == nil && id == job.ID {
		return true
	}
	return strings.Contains(strings.ToLower(job.Name), strings.ToLower(sel))
}

// matchesStep reports whether a section satisfies the --step selector: a step
// number, or a case-insensitive substring of the step name.
func matchesStep(s logSection, sel string) bool {
	if sel == "" {
		return true
	}
	if n, err := strconv.Atoi(sel); err == nil {
		return s.StepNumber == n
	}
	return strings.Contains(strings.ToLower(s.StepName), strings.ToLower(sel))
}

// collectSections gathers every log section of a run that the options select.
//
// It prefers the whole-run archive, which is the only source with step
// boundaries. That archive exists only for a finished run, so a run still in
// progress falls back to the per-job text logs, which GitHub publishes as soon
// as each individual job completes. The second return value reports whether
// step granularity was available.
func collectSections(repo string, runID int64, jobs []apiJob, opt collectOptions) ([]logSection, bool, error) {
	var selected []apiJob
	for _, j := range jobs {
		if !matchesJob(j, opt.Job) {
			continue
		}
		if opt.FailedOnly && !jobFailed(j) {
			continue
		}
		selected = append(selected, j)
	}
	if len(selected) == 0 {
		if opt.Job != "" {
			return nil, false, fmt.Errorf("no job of run %d matches %q", runID, opt.Job)
		}
		return nil, false, nil
	}

	haveSteps := false
	var sections []logSection

	if data, err := fetchRunLogZip(repo, runID); err == nil {
		files, err := readRunLogZip(data)
		if err != nil {
			return nil, false, err
		}
		sections = sectionsFromZip(files, selected)
		for _, s := range sections {
			if s.StepNumber > 0 {
				haveSteps = true
				break
			}
		}
	}

	if len(sections) == 0 {
		// The archive is not published yet. Every job that has finished still
		// has its own downloadable log, so read those instead. Those logs carry
		// step boundaries too, as BOMs, so a run in progress still answers
		// --step and --failed rather than dumping the whole job.
		for _, j := range selected {
			body, err := fetchJobLogText(repo, j.ID)
			if err != nil || strings.TrimSpace(body) == "" {
				continue
			}
			lines := splitLines(body)
			if steps := splitJobLog(j, lines); steps != nil {
				sections = append(sections, steps...)
				haveSteps = true
				continue
			}
			sections = append(sections, logSection{
				JobID: j.ID, JobName: j.Name, Conclusion: j.Conclusion,
				Lines: lines,
			})
		}
	}

	if opt.Step != "" {
		var kept []logSection
		for _, s := range sections {
			if matchesStep(s, opt.Step) {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			if !haveSteps {
				return nil, false, fmt.Errorf("step-level logs are not available yet: GitHub publishes them only after the whole run finishes")
			}
			return nil, haveSteps, fmt.Errorf("no step matches %q", opt.Step)
		}
		sections = kept
	}

	if opt.FailedOnly && haveSteps {
		var kept []logSection
		for _, s := range sections {
			if s.Failed() {
				kept = append(kept, s)
			}
		}
		// A job can fail without any single step being marked failed (a cancel,
		// or a failure in the runner itself). Keep the whole job in that case
		// rather than reporting nothing.
		if len(kept) > 0 {
			sections = kept
		}
	}

	return sections, haveSteps, nil
}

// jobFailed reports whether a job's conclusion is one worth reading the log for.
func jobFailed(j apiJob) bool {
	switch j.Conclusion {
	case "", "success", "skipped", "neutral":
		return false
	}
	return true
}
