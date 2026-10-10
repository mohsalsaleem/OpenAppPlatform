package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderRequiresExactCommitImmutableRepositoryAndBoundedOutput(t *testing.T) {
	r := BuildRequest{Commit: strings.Repeat("a", 40), Component: Component{ImageRepository: "registry.example/app"}}
	for _, output := range []string{`{"commit":"wrong","image":"registry.example/app:latest"}`, `{"commit":"` + r.Commit + `","image":"other.example/app@sha256:` + strings.Repeat("b", 64) + `"}`} {
		file := filepath.Join(t.TempDir(), "builder")
		if e := os.WriteFile(file, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0700); e != nil {
			t.Fatal(e)
		}
		if _, e := (CommandBuilder{Command: []string{file}}).Build(context.Background(), r); e == nil {
			t.Fatal("invalid builder result accepted")
		}
	}
	good, _ := json.Marshal(BuildResult{Commit: r.Commit, Image: r.Component.ImageRepository + "@sha256:" + strings.Repeat("b", 64)})
	file := filepath.Join(t.TempDir(), "builder")
	os.WriteFile(file, append([]byte("#!/bin/sh\nprintf '%s' '"), append(good, []byte("'\n")...)...), 0700)
	if _, e := (CommandBuilder{Command: []string{file}}).Build(context.Background(), r); e != nil {
		t.Fatal(e)
	}
}
