package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// target names the run a query subcommand operates on. Exactly one run is
// resolved, because every log query is scoped to a run.
type target struct {
	Repo string
	Run  apiRun
	Jobs []apiJob
}

// selector holds the flags every query subcommand shares for picking a run.
type selector struct {
	RunID    string // positional run ID, empty to resolve one
	SHA      string // commit to resolve the run from
	Workflow string // workflow file name or ID to narrow to
	Attempt  int    // run attempt, 0 for the latest
}

// resolveRepo returns the repository to query: the --repo value when given,
// otherwise the one the current directory belongs to.
func resolveRepo() (string, error) {
	if repoFlag != "" {
		return repoFlag, nil
	}
	if err := findGitRepo(); err != nil {
		return "", fmt.Errorf("%w (or pass --repo OWNER/REPO)", err)
	}
	out, err := runCommand("gh", "repo", "view", "--json", "nameWithOwner")
	if err != nil {
		return "", fmt.Errorf("could not determine the GitHub repository: %w", err)
	}
	var info RepoInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("could not parse the repository name: %w", err)
	}
	return info.NameWithOwner, nil
}

// resolveCommit returns the commit a run lookup should use, or "" when the
// lookup must not be scoped to a commit.
//
// An explicit --sha always wins. Without one, a local checkout contributes its
// own HEAD, so "gh wait-ci log" in a repository means "the run for what I have
// checked out". With --repo and no --sha there is no local commit to use, and
// the lookup falls back to the repository's most recent runs.
func resolveCommit(repo, sha string) string {
	if sha != "" {
		if repoFlag != "" {
			var c RemoteCommitInfo
			if err := ghAPIJSON(fmt.Sprintf("repos/%s/commits/%s", repo, sha), &c); err == nil {
				return c.SHA
			}
			return sha
		}
		full, err := runCommand("git", "rev-parse", sha)
		if err != nil {
			return sha
		}
		return full
	}
	if repoFlag != "" {
		return ""
	}
	full, err := runCommand("git", "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return full
}

// resolveTarget picks the run a subcommand acts on and loads its jobs.
func resolveTarget(sel selector) (*target, error) {
	repo, err := resolveRepo()
	if err != nil {
		return nil, err
	}

	var run *apiRun
	if sel.RunID != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(sel.RunID), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid run ID %q: a run ID is a number, as printed by `gh wait-ci runs`", sel.RunID)
		}
		run, err = fetchRun(repo, id)
		if err != nil {
			return nil, err
		}
	} else {
		commit := resolveCommit(repo, sel.SHA)
		runs, err := fetchRuns(repo, runFilter{Commit: commit, Workflow: sel.Workflow, Limit: 20})
		if err != nil {
			return nil, err
		}
		if len(runs) == 0 {
			if commit != "" {
				return nil, fmt.Errorf("no workflow run found for commit %s in %s", shortSHA(commit), repo)
			}
			return nil, fmt.Errorf("no workflow run found in %s", repo)
		}
		r := runs[0]
		run = &r
	}

	jobs, err := fetchJobs(repo, run.ID, sel.Attempt)
	if err != nil {
		return nil, err
	}
	return &target{Repo: repo, Run: *run, Jobs: jobs}, nil
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
