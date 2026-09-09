# gh-wait-ci

A GitHub CLI extension that waits for CI without polling spam. It also reads and searches everything a run produced.

It covers the full `gh run` surface. A repository or an agent can ban `gh run *` outright and lose nothing. See [Replacing `gh run`](#replacing-gh-run) for the command-by-command mapping.

## Installation

Every push publishes the binary to buildhost, which is where consumers take it from:

```bash
mkdir -p ~/.local/share/gh/extensions/gh-wait-ci
curl -fL --compressed "https://dl.pazer.build/gh-wait-ci?os=linux&arch=amd64" \
Builds go to [buildhost](https://pazer.build), not to GitHub Releases. There is nothing for `gh extension install` to resolve. A `gh` extension is a directory named `gh-<name>` that holds an executable of the same name. To put the binary there is the full install:

```bash
mkdir -p ~/.local/share/gh/extensions/gh-wait-ci
curl -fsSL "https://dl.pazer.build/gh-wait-ci?os=linux&arch=amd64" \
  -o ~/.local/share/gh/extensions/gh-wait-ci/gh-wait-ci
chmod +x ~/.local/share/gh/extensions/gh-wait-ci/gh-wait-ci
```

`os` takes `linux`, `darwin` or `windows`, and `arch` takes `amd64` or `arm64`.
Set `os` to `linux`, `darwin` or `windows`. Set `arch` to `amd64` or `arm64`. To use the tool as a plain command, put the same binary on `PATH`. Then run `gh-wait-ci` in place of `gh wait-ci`.

That URL serves a build older than this branch. The buildhost project keeps the GitHub owner that this repository had before it moved orgs. The publish gets HTTP 403 for that reason. CI therefore keeps the publish off. An operator must re-pin the project first. Until then, `go build .` is the only source of current code.

## Usage

```bash
# Wait for CI on current commit
gh wait-ci

# Wait for a specific commit
gh wait-ci --sha c79dcca

# Wait for a specific run by ID
gh wait-ci 12345678

# Give up after 5 minutes instead of the 30-minute default
gh wait-ci --timeout 5m

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
| `--timeout` | Give up waiting after this long (default `30m`, and `0` waits with no limit) |
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

`log` and `grep` both accept a run ID, `--sha`, `--workflow` and `--attempt` to pick which run they read. Both default to the run for the commit checked out.

A pattern matches against the CLEANED text. A pattern therefore never has to allow for the RFC3339 timestamp that GitHub puts on each line. It never has to allow for the `##[error]` markers around the output. Pass `--raw` to `log` to see the bytes as stored.

### Step-level output needs a finished run

A job's own log is available the moment that job completes. `log --job` therefore works while the rest of the run continues. The archive that carries STEP boundaries becomes available only after the full run finishes. `--step` reports that plainly instead of a guess at the boundaries.

### Annotations are not in the logs

`gh wait-ci annotations` shows the file-and-line errors and warnings that GitHub renders at the top of a run page. A workflow produces these separately. To read the logs alone can miss the one line that explains a failure.

## A run is not the only thing that gates a merge

```bash
# Every check on the checked-out commit: check runs AND commit statuses
gh wait-ci checks

# Only what is not green, which is what blocks the merge
gh wait-ci checks --failed
gh wait-ci checks --sha c79dcca --json
```

Three separate surfaces decide whether a commit is mergeable. The Actions API reports only the first of them. A workflow run is the first. A check run from another app is the second. A legacy COMMIT STATUS is the third. A required status such as `all-builds` is a commit status. It therefore appears in no run listing and in no check-run listing. `checks` reads all three surfaces off one commit.

The surfaces need different permissions. The Checks API is a GitHub App scope. A fine-grained PAT gets HTTP 403 there. The same PAT reads commit statuses correctly. `checks` reports that loss on stderr. It prints the surface that it can read. It does not fail and hide the half that names the required gate.

## Starting a run

```bash
gh wait-ci dispatch ci.yml
gh wait-ci dispatch release.yml --ref master --input level=debug
```

`dispatch` starts a run of a workflow that declares a `workflow_dispatch` trigger.

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
| `gh wait-ci checks` | Every check on a commit: check runs AND commit statuses |
| `gh wait-ci artifacts` | List a run's artifacts, and download them |
| `gh wait-ci workflows` | The repository's workflow definitions |
| `gh wait-ci dispatch` | Start a `workflow_dispatch` run |
| `gh wait-ci cancel` | Cancel a run |
| `gh wait-ci rerun` | Re-run a run, its failed jobs, or one job |

Every query command takes `--json`. A script therefore consumes the output without a parse of the human table.

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
| `gh workflow run ci.yml -f k=v` | `gh wait-ci dispatch ci.yml --input k=v` |
| `gh pr checks` | `gh wait-ci checks` |
| `gh api repos/O/R/commits/S/status` | `gh wait-ci checks --sha S --json` |
| `gh api repos/O/R/commits/S/check-runs` | `gh wait-ci checks --sha S --json` |
| `gh api repos/O/R/actions/runs/<id>` | `gh wait-ci view <id> --json` |
| `gh api repos/O/R/actions/runs/<id>/jobs` | `gh wait-ci jobs <id> --json` |

Four things have no `gh run` equivalent at all. These are `grep` over a run's logs, the `--step` filter, `annotations`, and the commit statuses that `checks` reports.

## Live logs (`--logs`)

By default `gh wait-ci` shows a live status summary. With `--logs` it also shows the actual log output.

- **Step progress is live.** Each job prints its steps as they start and finish, such as `✓ Build` and `✓ Run tests`. You watch progress in the terminal instead of a refresh of the Actions page.
- **Each job's full log prints the moment that job finishes.** Timestamps are stripped. The `##[group]` and `##[error]` markers are cleaned up. In a multi-job workflow the logs arrive one job at a time, as each job completes.

### Why logs appear per job, not line-by-line

GitHub's REST API makes a job's log available for download only after the job completes. While the job runs, the logs endpoint redirects to a storage blob that does not exist yet (HTTP 404). The line-by-line live log on github.com comes from the browser's authenticated web session. A token-based CLI cannot reuse that session. So `--logs` streams the most granular output that the GitHub API gives to a token. That output is the live step transitions, plus the full job log the instant each job finishes.

## What it does

1. Checks that you are in a git repository with pushed commits
2. Shows repository, branch, commit, and PR information
3. Finds workflow runs for the current commit, and retries when it finds none yet
4. Polls run status every few seconds, with no `sleep`-loop spam, until everything finishes
5. Streams step progress and job logs instead, when you pass `--logs`
6. Reports the final status with job details and links
7. Shows the failed-log commands when CI fails

Every wait is bounded. A queued job that no runner picks up never starts. An unbounded wait therefore blocks the caller forever. `--timeout` defaults to 30 minutes and exits non-zero. Its message names the command that reads the state the run reached. `--timeout 0` waits with no limit.

## Requirements

- `gh` CLI authenticated
