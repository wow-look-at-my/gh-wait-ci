# gh-wait-ci

A GitHub CLI extension to wait for CI to complete without polling spam, and to
read and search everything a run produced.

It covers the whole `gh run` surface, so a repository or agent can ban
`gh run *` outright and lose nothing. See
[Replacing `gh run`](#replacing-gh-run) for the command-by-command mapping.

## Installation

```bash
gh extension install PazerOP/gh-wait-ci
```

## Usage

```bash
# Wait for CI on current commit
gh wait-ci

# Wait for a specific commit
gh wait-ci --sha c79dcca

# Wait for a specific run by ID
gh wait-ci 12345678

# Wait for CI on a remote repo
gh wait-ci --repo owner/repo

# Wait for a specific commit on a remote repo
gh wait-ci --sha c79dcca --repo owner/repo

# Exit immediately on first failure
gh wait-ci --fail-fast

# Watch step-by-step progress live and print each job's log as it finishes
gh wait-ci --logs

# Stream logs and poll a little faster
gh wait-ci --logs --interval 3
```

## Flags

| Flag | Description |
| --- | --- |
| `-l`, `--logs` | Stream step progress live and print each job's full log as it finishes |
| `-i`, `--interval` | Polling interval in seconds (default `5`) |
| `--fail-fast` | Exit immediately when any job fails |
| `-s`, `--sha` | Commit SHA to watch (full or partial) |
| `-R`, `--repo` | Target repository in `[HOST/]OWNER/REPO` format |

## Reading and querying logs

```bash
# Everything a run logged
gh wait-ci log

# Only what failed, which is almost always the question
gh wait-ci log --failed

# One job, or one step of it
gh wait-ci log --job test
gh wait-ci log --job test --step "Run tests"

# The last 50 lines of each section, with no headers, for piping onward
gh wait-ci log --tail 50 --no-headers --plain

# Search, with grep's flags and grep's exit codes
gh wait-ci grep 'panic:'
gh wait-ci grep -i -C 3 'permission denied' --failed
gh wait-ci grep -F 'exit status 1' --count
gh wait-ci grep 'FAIL' --json | jq -r '.[].text'
```

`log` and `grep` both accept a run ID, `--sha`, `--workflow` and `--attempt` to
pick which run they read, and default to the run for the commit checked out.

Matching runs against the CLEANED text, so a pattern never has to allow for the
RFC3339 timestamp GitHub prefixes onto every line, nor for the `##[error]`
markers it wraps output in. Pass `--raw` to `log` to see the bytes as stored.

### Step-level output needs a finished run

A job's own log is downloadable the moment that job completes, so `log --job`
works while the rest of a run is still going. The archive that carries STEP
boundaries is published only once the whole run finishes, so `--step` reports
that plainly rather than guessing at boundaries.

### Annotations are not in the logs

`gh wait-ci annotations` shows the file-and-line errors and warnings GitHub
renders at the top of a run page. A workflow produces these separately, so
reading the logs alone can miss the one line that explains a failure.

## Commands

| Command | What it does |
| --- | --- |
| `gh wait-ci` | Wait for every run on the current commit, then report |
| `gh wait-ci watch` | The same thing, named |
| `gh wait-ci runs` | List workflow runs, with branch/event/status/actor/commit filters |
| `gh wait-ci view` | A run's summary, jobs, steps and timings |
| `gh wait-ci jobs` | A run's jobs with their IDs, states, durations and runners |
| `gh wait-ci log` | Print logs, filtered by job, step or outcome |
| `gh wait-ci grep` | Search logs for a pattern |
| `gh wait-ci annotations` | The errors and warnings that never reach the logs |
| `gh wait-ci artifacts` | List a run's artifacts, and download them |
| `gh wait-ci workflows` | The repository's workflow definitions |
| `gh wait-ci cancel` | Cancel a run |
| `gh wait-ci rerun` | Re-run a run, its failed jobs, or one job |

Every query command takes `--json`, so a script consumes it without parsing the
human table.

## Replacing `gh run`

| Instead of | Use |
| --- | --- |
| `gh run list` | `gh wait-ci runs` |
| `gh run list --branch X --workflow ci.yml` | `gh wait-ci runs --branch X --workflow ci.yml` |
| `gh run view <id>` | `gh wait-ci view <id>` |
| `gh run view <id> --json jobs` | `gh wait-ci view <id> --json` |
| `gh run view <id> --log` | `gh wait-ci log <id>` |
| `gh run view <id> --log-failed` | `gh wait-ci log <id> --failed` |
| `gh run view --log --job <job-id>` | `gh wait-ci log --job <job-id>` |
| `gh run view <id> --log \| grep X` | `gh wait-ci grep X <id>` |
| `gh run watch <id>` | `gh wait-ci <id>` |
| `gh run download <id>` | `gh wait-ci artifacts <id> --download all` |
| `gh run cancel <id>` | `gh wait-ci cancel <id>` |
| `gh run rerun <id> --failed` | `gh wait-ci rerun <id> --failed` |
| `gh workflow list` | `gh wait-ci workflows` |

Three things have no `gh run` equivalent at all: `grep` over a run's logs,
`--step` filtering, and `annotations`.

## Live logs (`--logs`)

By default `gh wait-ci` shows a live status summary. With `--logs` it also shows
the actual log output:

- **Step progress is live.** As each job runs, its steps print as they start and
  finish (`✓ Build`, `✓ Run tests`, ...), so you can watch progress in the
  terminal instead of refreshing the Actions page.
- **Each job's full log prints the moment that job finishes**, with timestamps
  stripped and `##[group]` / `##[error]` markers cleaned up. In a multi-job
  workflow the logs arrive progressively, one job at a time, as each completes.

### Why logs appear per job, not line-by-line

GitHub's REST API only makes a job's log downloadable once the job has
**completed** — while it runs, the logs endpoint redirects to a storage blob
that doesn't exist yet (HTTP 404). The line-by-line live log you see on
github.com is rendered from the browser's authenticated web session, which a
token-based CLI can't reuse. So `--logs` streams the most granular output the
GitHub API actually exposes to a token: step transitions live, and the full job
log the instant each job finishes.

## What it does

1. Checks you're in a git repo with pushed commits
2. Shows repo, branch, commit, and PR info
3. Finds workflow runs for the current commit (retries if not found yet)
4. Polls run status every few seconds (no `sleep`-loop spam) until everything
   finishes — or, with `--logs`, streams step progress and job logs as above
5. Reports final status with job details and links
6. Shows failed-log commands if CI failed

## Requirements

- `gh` CLI authenticated
