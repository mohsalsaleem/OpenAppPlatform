package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentCommandsGolden pins every agent API command in each output
// mode: human text, --json and --events, against one simulated server.
func TestAgentCommandsGolden(t *testing.T) {
	ts, _, _ := stack(t)
	t.Setenv("OAP_AGENT_URL", ts.URL)
	file := filepath.Join(t.TempDir(), "oap.yaml")
	os.WriteFile(file, []byte(parityYAML), 0o600)
	steps := []struct {
		name string
		args []string
	}{
		{"observe-empty.txt", []string{"observe"}},
		{"apply-human.txt", []string{"app", "apply", file, "--yes"}},
		{"apply-again.txt", []string{"app", "apply", file, "--yes"}},
		{"scale-json.txt", []string{"app", "scale", "site", "web", "2", "--yes", "--json"}},
		{"scale-events.txt", []string{"app", "scale", "site", "web", "3", "--yes", "--events"}},
		{"scale-unapproved-json.txt", []string{"app", "scale", "site", "web", "1", "--json"}},
		{"observe-human.txt", []string{"observe", "site"}},
		{"observe-json.txt", []string{"observe", "site", "--json"}},
		{"explain-human.txt", []string{"explain", "site"}},
		{"explain-json.txt", []string{"explain", "site", "--json"}},
		{"watch-human.txt", []string{"watch", "--since", "1", "--limit", "8"}},
		{"watch-events.txt", []string{"watch", "site", "--since", "1", "--limit", "4", "--events"}},
	}
	for _, s := range steps {
		h := newHarness(t, false, "", "")
		code := h.run(s.args...)
		out := strings.ReplaceAll(redact(h.out.String(), file), ts.URL, "$OAP_AGENT_URL")
		golden(t, "cli/"+s.name, fmt.Sprintf("$ oapctl %s\nexit %d\n%s", redact(strings.Join(s.args, " "), file), code, out))
	}
}

// redact replaces the temporary file path, which differs per run.
func redact(s, file string) string { return strings.ReplaceAll(s, file, "oap.yaml") }
