package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The remote is what names the repository now. gh refuses to answer when
// GH_HOST points at a mirror no remote URL carries, which made every
// subcommand unusable without --repo in a mirrored environment.
func TestParseRemoteRepo(t *testing.T) {
	cases := []struct {
		name, remote, want string
	}{
		{"https", "https://github.com/wow-look-at-my/lpi", "wow-look-at-my/lpi"},
		{"https with suffix", "https://github.com/wow-look-at-my/lpi.git", "wow-look-at-my/lpi"},
		{"trailing slash", "https://github.com/wow-look-at-my/lpi/", "wow-look-at-my/lpi"},
		{"newline from git", "https://github.com/wow-look-at-my/lpi\n", "wow-look-at-my/lpi"},
		{"scp-like", "git@github.com:wow-look-at-my/lpi.git", "wow-look-at-my/lpi"},
		{"ssh url", "ssh://git@github.com/wow-look-at-my/lpi.git", "wow-look-at-my/lpi"},
		{"host with port", "https://ghe.example.com:8443/acme/tool.git", "acme/tool"},
		{"proxy path prefix", "http://local_proxy@127.0.0.1:44617/git/wow-look-at-my/lpi", "wow-look-at-my/lpi"},
		{"repository name with a dot", "https://github.com/acme/tool.io.git", "acme/tool.io"},
		{"no repository segment", "https://github.com/wow-look-at-my", ""},
		{"empty", "", ""},
		{"not a url", "origin", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, parseRemoteRepo(c.remote))
		})
	}
}

// An owner is letters, digits and hyphens. That is what tells a real pair apart
// from a trailing host plus owner on a URL naming no repository.
func TestIsOwnerName(t *testing.T) {
	assert.True(t, isOwnerName("wow-look-at-my"))
	assert.True(t, isOwnerName("PazerOP"))
	assert.True(t, isOwnerName("user123"))
	assert.False(t, isOwnerName("github.com"))
	assert.False(t, isOwnerName("127.0.0.1"))
	assert.False(t, isOwnerName(""))
	assert.False(t, isOwnerName("has space"))
}
