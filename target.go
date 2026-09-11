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
	// The remote answers before gh does. gh refuses to name the repository at
	// all when GH_HOST points at a mirror that no remote URL carries, and it
	// says so as "none of the git remotes ... correspond to the GH_HOST
	// environment variable". That makes every subcommand unusable without
	// --repo in a mirrored environment. A remote URL cannot disagree with
	// itself, so it is the better source.
	if repo, err := repoFromGitRemote(); err == nil {
		return repo, nil
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

// repoFromGitRemote reads OWNER/REPO off this checkout's remotes, preferring
// origin. It asks git rather than gh, so no host setting can refuse it.
func repoFromGitRemote() (string, error) {
	names := []string{"origin"}
	if out, err := runCommand("git", "remote"); err == nil {
		for _, n := range strings.Fields(out) {
			if n != "origin" {
				names = append(names, n)
			}
		}
	}
	for _, name := range names {
		out, err := runCommand("git", "remote", "get-url", name)
		if err != nil {
			continue
		}
		if repo := parseRemoteRepo(out); repo != "" {
			return repo, nil
		}
	}
	return "", fmt.Errorf("no git remote carries an OWNER/REPO path")
}

// parseRemoteRepo takes OWNER/REPO off a remote URL. The last two path
// segments carry it in every spelling git accepts: the https form, the
// scp-like form, and a proxy URL that prefixes a path of its own. An owner is
// letters, digits and hyphens, which is what tells that pair apart from a
// trailing host plus owner on a URL naming no repository.
func parseRemoteRepo(remote string) string {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	// A colon separates host from path in the scp-like form, and port from host
	// elsewhere. Either way the trailing pair is unaffected.
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[len(parts)-2], parts[len(parts)-1]
	if repo == "" || !isOwnerName(owner) {
		return ""
	}
	return owner + "/" + repo
}

// isOwnerName reports whether s spells a GitHub account: letters, digits and
// hyphens, nothing else. A host name carries a dot and fails here.
func isOwnerName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
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
	// The run ID is checked before anything reaches the network, so a typo is
	// reported as the typo it is rather than as whatever the repository lookup
	// happens to fail with.
	var wantRun int64
	if sel.RunID != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(sel.RunID), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid run ID %q: a run ID is a number, as printed by `gh wait-ci runs`", sel.RunID)
		}
		wantRun = id
	}

	repo, err := resolveRepo()
	if err != nil {
		return nil, err
	}

	var run *apiRun
	if wantRun != 0 {
		run, err = fetchRun(repo, wantRun)
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
