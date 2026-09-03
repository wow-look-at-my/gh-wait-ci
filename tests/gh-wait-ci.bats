#!/usr/bin/env bats

# Timeout for each test (seconds)
TEST_TIMEOUT=5

setup() {
    # Add mocks to PATH
    export PATH="$BATS_TEST_DIRNAME/mocks:$PATH"
    chmod +x "$BATS_TEST_DIRNAME/mocks/gh" "$BATS_TEST_DIRNAME/mocks/git"
    # go-toolchain writes the binary to build/; a plain `go build -o` run puts it
    # at the repo root. Honour an explicit path over both.
    if [[ -z "${GH_WAIT_CI_BIN:-}" ]]; then
        GH_WAIT_CI_BIN="$BATS_TEST_DIRNAME/../gh-wait-ci"
        [[ -x "$GH_WAIT_CI_BIN" ]] || GH_WAIT_CI_BIN="$BATS_TEST_DIRNAME/../build/gh-wait-ci"
    fi
    export GH_WAIT_CI_BIN
}

# Wrapper with timeout
run_script() {
    timeout "$TEST_TIMEOUT" "$GH_WAIT_CI_BIN" "$@"
}

@test "shows commands for failed logs instead of inline logs" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "failure", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "failure",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [
            {"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111},
            {"name": "test", "status": "completed", "conclusion": "failure", "databaseId": 222}
        ]
    }'

    run run_script

    # Should show command hints inline with failed jobs, and never point at
    # `gh run`, which this tool exists to replace.
    [[ "$output" == *"test"*"→"*"gh wait-ci log 12345 --job 222"* ]]
    [[ "$output" == *"View all failed logs"* ]]
    [[ "$output" == *"gh wait-ci log 12345 --failed"* ]]
    [[ "$output" == *"gh wait-ci grep"* ]]
    [[ "$output" == *"gh wait-ci annotations 12345"* ]]
    [[ "$output" != *"gh run view"* ]]
}

@test "shows progress percentage" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [
            {"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111},
            {"name": "test", "status": "completed", "conclusion": "success", "databaseId": 222}
        ]
    }'

    run run_script

    # Should show progress with percentage
    [[ "$output" == *"Progress: 2/2 (100%)"* ]]
}

@test "shows all job statuses" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'

    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [
            {"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111},
            {"name": "test", "status": "completed", "conclusion": "success", "databaseId": 222},
            {"name": "lint", "status": "completed", "conclusion": "skipped", "databaseId": 333}
        ]
    }'

    run run_script

    # Should show all jobs with their statuses
    [[ "$output" == *"build"* ]]
    [[ "$output" == *"test"* ]]
    [[ "$output" == *"lint"* ]]
    [[ "$output" == *"skipped"* ]]
}

@test "successful run shows PASSED" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'

    run run_script

    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

@test "failed run shows FAILED and exits non-zero" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "failure", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "failure",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "failure", "databaseId": 111}]
    }'

    run run_script

    [[ "$output" == *"FAILED"* ]]
    [[ "$status" -ne 0 ]]
}

@test "--repo flag targets specified repository" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 99999, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/other-owner/other-repo/actions/runs/99999",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'
    export MOCK_API_REPO_JSON='{"default_branch": "main"}'
    export MOCK_API_COMMITS_JSON='{"sha": "def456abc789012"}'

    run run_script --repo other-owner/other-repo

    [[ "$output" == *"other-owner/other-repo"* ]]
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

@test "-R short flag works same as --repo" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 99999, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/other-owner/other-repo/actions/runs/99999",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'
    export MOCK_API_REPO_JSON='{"default_branch": "main"}'
    export MOCK_API_COMMITS_JSON='{"sha": "def456abc789012"}'

    run run_script -R other-owner/other-repo

    [[ "$output" == *"other-owner/other-repo"* ]]
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

@test "--repo flag with run-id targets specified repository" {
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/other-owner/other-repo/actions/runs/55555",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'

    run run_script --repo other-owner/other-repo 55555

    [[ "$output" == *"other-owner/other-repo"* ]]
    [[ "$output" == *"Watching specified run: 55555"* ]]
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

@test "shows commit and PR links" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'

    run run_script

    [[ "$output" == *"Commit:"* ]]
    [[ "$output" == *"PR:"* ]]
    [[ "$output" == *"github.com"* ]]
}

@test "auto-discovers single git repo in subdirectory" {
    export MOCK_GIT_NOT_IN_REPO=true
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'

    TMPDIR=$(mktemp -d)
    mkdir -p "$TMPDIR/my-project/.git"

    cd "$TMPDIR"
    run "$GH_WAIT_CI_BIN"

    [[ "$output" == *"Found git repository in ./my-project"* ]]
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]

    rm -rf "$TMPDIR"
}

@test "errors with list when multiple git repos found in subdirectories" {
    export MOCK_GIT_NOT_IN_REPO=true

    TMPDIR=$(mktemp -d)
    mkdir -p "$TMPDIR/repo-a/.git"
    mkdir -p "$TMPDIR/repo-b/.git"

    cd "$TMPDIR"
    run "$GH_WAIT_CI_BIN"

    [[ "$output" == *"multiple repositories"* ]]
    [[ "$output" == *"repo-a"* ]]
    [[ "$output" == *"repo-b"* ]]
    [[ "$status" -ne 0 ]]

    rm -rf "$TMPDIR"
}

@test "errors when not in git repo and no subdirectory repos found" {
    export MOCK_GIT_NOT_IN_REPO=true

    TMPDIR=$(mktemp -d)

    cd "$TMPDIR"
    run "$GH_WAIT_CI_BIN"

    [[ "$output" == *"not in a git repository"* ]]
    [[ "$status" -ne 0 ]]

    rm -rf "$TMPDIR"
}

@test "--sha flag uses specified commit" {
    export MOCK_REV_PARSE="deadbeef12345678"
    export MOCK_REV_PARSE_SHORT="deadbee"
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'

    run run_script --sha deadbeef12345678

    [[ "$output" == *"deadbee"* ]]
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

@test "--sha with --repo uses specified commit on remote" {
    export MOCK_API_COMMITS_JSON='{"sha": "deadbeef12345678"}'
    export MOCK_RUN_LIST_JSON='[{"databaseId": 99999, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/other-owner/other-repo/actions/runs/99999",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'

    run run_script --sha deadbeef --repo other-owner/other-repo

    [[ "$output" == *"other-owner/other-repo"* ]]
    [[ "$output" == *"deadbee"* ]]
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

@test "unknown flag prints error message" {
    run run_script --bogus

    [[ "$output" == *"unknown flag"* ]]
    [[ "$status" -ne 0 ]]
}

@test "--logs streams job log content with timestamps stripped" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]
    }'
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z Hello from CI
2026-06-01T02:05:02.7654321Z ##[group]Run the build
2026-06-01T02:05:03.0000000Z compiling everything
2026-06-01T02:05:04.0000000Z ##[endgroup]'

    run run_script --logs

    # Log content is shown
    [[ "$output" == *"Hello from CI"* ]]
    [[ "$output" == *"compiling everything"* ]]
    # Raw RFC3339 timestamps are stripped
    [[ "$output" != *"2026-06-01T02:05:01"* ]]
    # Workflow command markers are normalized away
    [[ "$output" != *"##[group]"* ]]
    [[ "$output" != *"##[endgroup]"* ]]
    [[ "$output" == *"Run the build"* ]]
    # Still reports final status and exits cleanly
    [[ "$output" == *"PASSED"* ]]
    [[ "$status" -eq 0 ]]
}

#
# Query subcommands. Together these are what makes `gh run *` unnecessary, so
# each one is exercised end to end against the mock API.
#

# Shared fixtures: one finished run with a passing and a failing job.
setup_run_fixtures() {
    export MOCK_API_RUNS_JSON='{"workflow_runs": [
        {"id": 12345, "name": "CI", "status": "completed", "conclusion": "failure",
         "head_branch": "claude/thing", "head_sha": "abc123def456789", "event": "push",
         "run_number": 7, "run_attempt": 1, "created_at": "2026-06-01T00:00:00Z",
         "updated_at": "2026-06-01T00:03:00Z", "actor": {"login": "someone"},
         "html_url": "https://github.com/test-owner/test-repo/actions/runs/12345"}
    ]}'
    export MOCK_API_RUN_JSON='{"id": 12345, "name": "CI", "status": "completed", "conclusion": "failure",
        "head_branch": "claude/thing", "head_sha": "abc123def456789", "event": "push",
        "run_number": 7, "run_attempt": 1, "created_at": "2026-06-01T00:00:00Z",
        "updated_at": "2026-06-01T00:03:00Z", "actor": {"login": "someone"},
        "html_url": "https://github.com/test-owner/test-repo/actions/runs/12345"}'
    export MOCK_API_JOBS_JSON='{"jobs": [
        {"id": 111, "run_id": 12345, "name": "build", "status": "completed", "conclusion": "success",
         "started_at": "2026-06-01T00:00:10Z", "completed_at": "2026-06-01T00:01:10Z",
         "runner_name": "ubuntu-latest",
         "steps": [{"number": 1, "name": "Set up job", "status": "completed", "conclusion": "success"}]},
        {"id": 222, "run_id": 12345, "name": "test", "status": "completed", "conclusion": "failure",
         "started_at": "2026-06-01T00:01:00Z", "completed_at": "2026-06-01T00:03:00Z",
         "runner_name": "ubuntu-latest",
         "steps": [{"number": 2, "name": "Run tests", "status": "completed", "conclusion": "failure"}]}
    ]}'
}

@test "runs lists workflow runs" {
    setup_run_fixtures

    run run_script runs

    [[ "$output" == *"RUN-ID"* ]]
    [[ "$output" == *"12345"* ]]
    [[ "$output" == *"CI"* ]]
    [[ "$output" == *"claude/thing"* ]]
    [[ "$status" -eq 0 ]]
}

@test "runs --json emits machine-readable output" {
    setup_run_fixtures

    run run_script runs --json

    [[ "$output" == *'"id": 12345'* ]]
    [[ "$status" -eq 0 ]]
}

@test "view shows the run summary with its jobs and steps" {
    setup_run_fixtures

    run run_script view 12345

    [[ "$output" == *"run 12345"* ]]
    [[ "$output" == *"build"* ]]
    [[ "$output" == *"test"* ]]
    [[ "$output" == *"Run tests"* ]]
    [[ "$status" -eq 0 ]]
}

@test "jobs --failed lists only the jobs that did not succeed" {
    setup_run_fixtures

    run run_script jobs 12345 --failed

    [[ "$output" == *"222"* ]]
    [[ "$output" == *"test"* ]]
    [[ "$output" != *"build"* ]]
    [[ "$status" -eq 0 ]]
}

@test "log prints job logs with timestamps and markers cleaned up" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z Hello from CI
2026-06-01T02:05:02.7654321Z ##[error]it broke'

    run run_script log 12345

    [[ "$output" == *"Hello from CI"* ]]
    [[ "$output" == *"it broke"* ]]
    [[ "$output" != *"2026-06-01T02:05:01"* ]]
    [[ "$output" != *"##[error]"* ]]
    [[ "$status" -eq 0 ]]
}

@test "log --raw keeps the timestamps and markers" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z ##[error]it broke'

    run run_script log 12345 --raw

    [[ "$output" == *"2026-06-01T02:05:01"* ]]
    [[ "$output" == *"##[error]"* ]]
    [[ "$status" -eq 0 ]]
}

@test "log --job narrows to one job by name" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z a log line'

    run run_script log 12345 --job test

    [[ "$output" == *"test"* ]]
    [[ "$output" != *"══ build"* ]]
    [[ "$status" -eq 0 ]]
}

@test "log --job rejects a name matching no job" {
    setup_run_fixtures

    run run_script log 12345 --job nosuchjob

    [[ "$output" == *"no job"* ]]
    [[ "$status" -ne 0 ]]
}

@test "grep finds a pattern and reports the job it came from" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z all good
2026-06-01T02:05:02.7654321Z ##[error]FAIL: TestThing'

    run run_script grep --plain FAIL 12345

    [[ "$output" == *"FAIL: TestThing"* ]]
    [[ "$output" == *"══ build"* ]]
    [[ "$status" -eq 0 ]]
}

@test "grep exits non-zero and prints nothing when nothing matches" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z all good'

    run run_script grep --plain no-such-text 12345

    [[ "$status" -ne 0 ]]
    [[ "$output" != *"ERROR"* ]]
}

@test "grep --count reports per-section totals" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z boom
2026-06-01T02:05:02.7654321Z boom again'

    run run_script grep --count boom 12345

    [[ "$output" == *"build: 2"* ]]
    [[ "$status" -eq 0 ]]
}

@test "grep --json emits structured matches" {
    setup_run_fixtures
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z boom'

    run run_script grep --json boom 12345

    [[ "$output" == *'"text": "boom"'* ]]
    [[ "$output" == *'"job": "build"'* ]]
    [[ "$status" -eq 0 ]]
}

@test "annotations shows the errors GitHub extracted" {
    setup_run_fixtures
    export MOCK_API_ANNOTATIONS_JSON='[{"path": "main.go", "start_line": 12, "end_line": 12,
        "annotation_level": "failure", "title": "vet", "message": "undefined: foo"}]'

    run run_script annotations 12345

    [[ "$output" == *"main.go:12"* ]]
    [[ "$output" == *"undefined: foo"* ]]
    [[ "$status" -eq 0 ]]
}

@test "annotations says so plainly when there are none" {
    setup_run_fixtures

    run run_script annotations 12345

    [[ "$output" == *"no annotations"* ]]
    [[ "$status" -eq 0 ]]
}

@test "artifacts lists a run's artifacts" {
    setup_run_fixtures
    export MOCK_API_ARTIFACTS_JSON='{"artifacts": [
        {"id": 99, "name": "coverage", "size_in_bytes": 2048, "expired": false,
         "created_at": "2026-06-01T00:03:00Z"}
    ]}'

    run run_script artifacts 12345

    [[ "$output" == *"coverage"* ]]
    [[ "$output" == *"2.0KB"* ]]
    [[ "$status" -eq 0 ]]
}

@test "workflows lists the repository's workflows" {
    export MOCK_API_WORKFLOWS_JSON='{"workflows": [
        {"id": 1, "name": "CI", "path": ".github/workflows/ci.yml", "state": "active"}
    ]}'

    run run_script workflows

    [[ "$output" == *"ci.yml"* ]]
    [[ "$output" == *"active"* ]]
    [[ "$status" -eq 0 ]]
}

@test "cancel posts to the cancel endpoint" {
    setup_run_fixtures

    run run_script cancel 12345

    [[ "$output" == *"Cancelled run 12345"* ]]
    [[ "$status" -eq 0 ]]
}

@test "rerun --failed re-runs only the failed jobs" {
    setup_run_fixtures

    run run_script rerun 12345 --failed

    [[ "$output" == *"failed jobs of run 12345"* ]]
    [[ "$status" -eq 0 ]]
}

@test "--logs prefixes lines with job name when multiple jobs run" {
    export MOCK_RUN_LIST_JSON='[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
    export MOCK_RUN_VIEW_JSON='{
        "status": "completed",
        "conclusion": "success",
        "name": "CI",
        "url": "https://github.com/test-owner/test-repo/actions/runs/12345",
        "jobs": [
            {"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111},
            {"name": "test", "status": "completed", "conclusion": "success", "databaseId": 222}
        ]
    }'
    export MOCK_JOB_LOG='2026-06-01T02:05:01.1234567Z a log line'

    run run_script --logs

    # Each job's lines are tagged with "RunName / JobName"
    [[ "$output" == *"CI / build"* ]]
    [[ "$output" == *"CI / test"* ]]
    [[ "$output" == *"a log line"* ]]
    [[ "$status" -eq 0 ]]
}
