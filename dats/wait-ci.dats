# What `gh wait-ci` prints, against a gh and a git that answer from fixtures.
#
# Every test drives the real binary through the real argv, with mocks ahead of
# it on PATH; nothing here reaches a network or a repository. The one thing
# that matters most is the last test: a red run must print the FAILURE, not a
# command that would show it. A reader who has to run something else to learn
# why a build broke reaches for the raw API and hand-rolls a poll loop around
# it, which is the habit this output exists to remove.
#
# go-toolchain runs this sandboxed on every build.

shared:
	copy:
		gh: ../tests/mocks/gh
		git: ../tests/mocks/git
	files:
		run.sh: |
			# Run the binary with the mocks ahead of it on PATH. The caller
			# exports whatever MOCK_* the case needs. The sandbox binds the
			# working directory read-only, so the built binary is reachable
			# at its ordinary path.
			set -eu
			PATH="$(dirname "{shared.gh}"):$PATH"
			export PATH
			exec ./build/gh-wait-ci "$@"

tests:
	- desc: a passing run reports PASSED and exits 0
	  cmd: sh {shared.run.sh}
	  inputs:
		env:
			MOCK_RUN_LIST_JSON: '[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
			MOCK_RUN_VIEW_JSON: '{"status": "completed", "conclusion": "success", "name": "CI", "url": "https://github.com/test-owner/test-repo/actions/runs/12345", "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]}'
	  outputs:
		stdout:
			- "PASSED"

	- desc: a failing run reports FAILED and exits non-zero
	  cmd: sh {shared.run.sh}
	  exit: 1
	  inputs:
		env:
			MOCK_RUN_LIST_JSON: '[{"databaseId": 12345, "status": "completed", "conclusion": "failure", "name": "CI"}]'
			MOCK_RUN_VIEW_JSON: '{"status": "completed", "conclusion": "failure", "name": "CI", "url": "https://github.com/test-owner/test-repo/actions/runs/12345", "jobs": [{"name": "build", "status": "completed", "conclusion": "failure", "databaseId": 111}]}'
	  outputs:
		stderr:
			- "FAILED"

	- desc: every job is listed, whatever its conclusion
	  cmd: sh {shared.run.sh}
	  inputs:
		env:
			MOCK_RUN_LIST_JSON: '[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
			MOCK_RUN_VIEW_JSON: '{"status": "completed", "conclusion": "success", "name": "CI", "url": "https://github.com/test-owner/test-repo/actions/runs/12345", "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}, {"name": "test", "status": "completed", "conclusion": "success", "databaseId": 222}, {"name": "lint", "status": "completed", "conclusion": "skipped", "databaseId": 333}]}'
	  outputs:
		stdout:
			- "build"
			- "test"
			- "lint"
			- "Progress: 3/3 (100%)"

	- desc: the commit and PR links are printed
	  cmd: sh {shared.run.sh}
	  inputs:
		env:
			MOCK_RUN_LIST_JSON: '[{"databaseId": 12345, "status": "completed", "conclusion": "success", "name": "CI"}]'
			MOCK_RUN_VIEW_JSON: '{"status": "completed", "conclusion": "success", "name": "CI", "url": "https://github.com/test-owner/test-repo/actions/runs/12345", "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}]}'
	  outputs:
		stdout:
			- "Commit:"
			- "PR:"
			- "github.com"

	# The point of the whole tool: the error text is IN the output. Asserting
	# the marker line means a run that prints only a hint fails here.
	- desc: a failed job prints its error lines, not a command to fetch them
	  cmd: sh {shared.run.sh}
	  exit: 1
	  inputs:
		env:
			MOCK_RUN_LIST_JSON: '[{"databaseId": 12345, "status": "completed", "conclusion": "failure", "name": "CI"}]'
			MOCK_RUN_VIEW_JSON: '{"status": "completed", "conclusion": "failure", "name": "CI", "url": "https://github.com/test-owner/test-repo/actions/runs/12345", "jobs": [{"name": "build", "status": "completed", "conclusion": "success", "databaseId": 111}, {"name": "test", "status": "completed", "conclusion": "failure", "databaseId": 222}]}'
			MOCK_JOB_LOG_222: |
				2026-01-01T00:00:00Z ordinary chatter nobody needs
				2026-01-01T00:00:01Z ##[error]undefined: cheeseburger
				2026-01-01T00:00:02Z more chatter
	  outputs:
		stdout:
			- "##[error]undefined: cheeseburger"
		!stdout:
			- "ordinary chatter nobody needs"

	# A log the markers do not match still has to say something: the tail is
	# where a build that died usually explains itself.
	- desc: an unrecognized failure log falls back to its tail
	  cmd: sh {shared.run.sh}
	  exit: 1
	  inputs:
		env:
			MOCK_RUN_LIST_JSON: '[{"databaseId": 12345, "status": "completed", "conclusion": "failure", "name": "CI"}]'
			MOCK_RUN_VIEW_JSON: '{"status": "completed", "conclusion": "failure", "name": "CI", "url": "https://github.com/test-owner/test-repo/actions/runs/12345", "jobs": [{"name": "test", "status": "completed", "conclusion": "failure", "databaseId": 222}]}'
			MOCK_JOB_LOG_222: |
				the build stopped and said nothing a marker matches
				but this last line is still the reason
	  outputs:
		stdout:
			- "but this last line is still the reason"