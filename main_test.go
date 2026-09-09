package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findCmd(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q is not registered", name)
	return nil
}

// Every `gh run` capability must have a home here, or the ban leaves a hole.
func TestEverySubcommandIsRegistered(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{
		"watch", "runs", "view", "jobs", "log", "grep",
		"annotations", "artifacts", "workflows", "cancel", "rerun",
	} {
		findCmd(t, root, name)
	}
}

// --repo is persistent so it reaches every subcommand, not just the bare wait.
func TestRepoFlagReachesEverySubcommand(t *testing.T) {
	root := newRootCmd()
	require.NotNil(t, root.PersistentFlags().Lookup("repo"))
	for _, c := range root.Commands() {
		assert.NotNil(t, c.InheritedFlags().Lookup("repo"), "%s cannot target another repository", c.Name())
	}
}

// -R advertises [HOST/]OWNER/REPO. The host has to come off before a path is
// built from it: `repos/HOST/OWNER/REPO` is a path of the wrong shape, and the
// API answers 404 rather than naming the mistake.
func TestRepoFlagSplitsTheHostOffThePath(t *testing.T) {
	host, repo := splitRepoTarget("github-state-mirror.example.com/wow-look-at-my/actions")
	assert.Equal(t, "github-state-mirror.example.com", host)
	assert.Equal(t, "wow-look-at-my/actions", repo)
}

// A bare OWNER/REPO keeps its old meaning: no host, and gh picks the default.
func TestABareRepoFlagNamesNoHost(t *testing.T) {
	host, repo := splitRepoTarget("wow-look-at-my/actions")
	assert.Empty(t, host)
	assert.Equal(t, "wow-look-at-my/actions", repo)
}

// The `gh` subcommands take the host on -R themselves, so what the user typed
// has to survive the split.
func TestRepoTargetRebuildsWhatTheUserTyped(t *testing.T) {
	previousHost, previousRepo := repoHost, repoFlag
	t.Cleanup(func() { repoHost, repoFlag = previousHost, previousRepo })

	repoHost, repoFlag = splitRepoTarget("example.com/wow-look-at-my/actions")
	assert.Equal(t, "example.com/wow-look-at-my/actions", repoTarget())

	repoHost, repoFlag = splitRepoTarget("wow-look-at-my/actions")
	assert.Equal(t, "wow-look-at-my/actions", repoTarget())
}

func TestLogAndGrepShareTheSameFilters(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"log", "grep"} {
		c := findCmd(t, root, name)
		for _, flag := range []string{"job", "step", "failed", "sha", "workflow", "attempt"} {
			assert.NotNil(t, c.Flags().Lookup(flag), "%s is missing --%s", name, flag)
		}
	}
}

func TestGrepAcceptsAPatternAndAnOptionalRunID(t *testing.T) {
	grep := findCmd(t, newRootCmd(), "grep")
	require.Error(t, grep.Args(grep, []string{}), "a pattern is required")
	require.NoError(t, grep.Args(grep, []string{"boom"}))
	require.NoError(t, grep.Args(grep, []string{"boom", "12345"}))
	require.Error(t, grep.Args(grep, []string{"boom", "12345", "extra"}))
}

func TestQueryCommandsOfferJSONOutput(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"runs", "view", "jobs", "grep", "annotations", "artifacts", "workflows"} {
		c := findCmd(t, root, name)
		assert.NotNil(t, c.Flags().Lookup("json"), "%s cannot be consumed by a script", name)
	}
}

func TestSelectorFromReadsThePositionalRunIDAndTheFlags(t *testing.T) {
	cmd := findCmd(t, newRootCmd(), "log")
	require.NoError(t, cmd.Flags().Set("sha", "deadbee"))
	require.NoError(t, cmd.Flags().Set("workflow", "ci.yml"))
	require.NoError(t, cmd.Flags().Set("attempt", "2"))

	sel := selectorFrom(cmd, []string{"98765"})
	assert.Equal(t, selector{RunID: "98765", SHA: "deadbee", Workflow: "ci.yml", Attempt: 2}, sel)

	assert.Empty(t, selectorFrom(cmd, nil).RunID)
}

func TestResolveTargetRejectsANonNumericRunID(t *testing.T) {
	_, err := resolveTarget(selector{RunID: "not-a-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid run ID")
}
