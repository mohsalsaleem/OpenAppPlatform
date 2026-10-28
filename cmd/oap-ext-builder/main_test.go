package main

import (
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension/dockerapi"
)

// TestRepositoriesAreRemote (R5.8): the builder clones only remote
// repositories unless the owner allows local ones; a local path could
// otherwise copy oapd's own files (its desired state, for one) into an
// image an agent can read.
func TestRepositoriesAreRemote(t *testing.T) {
	for repo, ok := range map[string]bool{
		"https://github.com/acme/shop.git": true, "ssh://git@github.com/acme/shop": true, "git@github.com:acme/shop.git": true,
		"file:///var/lib/oapd/desired": false, "/var/lib/oapd/desired": false, "ext::sh -c id": false, "../desired": false,
	} {
		if err := dockerapi.CheckRepository(repo, false); (err == nil) != ok {
			t.Errorf("%s: %v", repo, err)
		}
	}
	if err := dockerapi.CheckRepository("file:///tmp/repo", true); err != nil {
		t.Errorf("allowed local repository: %v", err)
	}
}
