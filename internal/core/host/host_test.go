package host

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestCheckGolden judges every recorded probe output. Fixtures come from
// real hosts (vultr-oap), read-only containers (container-*) and
// hand-written hosts (synthetic-*).
func TestCheckGolden(t *testing.T) {
	probes, _ := filepath.Glob("testdata/probes/*.txt")
	if len(probes) < 6 {
		t.Fatalf("found %d probe fixtures", len(probes))
	}
	for _, p := range probes {
		name := strings.TrimSuffix(filepath.Base(p), ".txt")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseProbe(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			blocked, stopped := Blocking(Check(f))
			got, _ := json.MarshalIndent(struct {
				Summary  string    `json:"summary"`
				Blocked  bool      `json:"blocked"`
				Stopped  bool      `json:"stopped"`
				Findings []Finding `json:"findings"`
			}{Summary(f), blocked, stopped, Check(f)}, "", "  ")
			golden := filepath.Join("testdata/findings", name+".json")
			if *update {
				os.MkdirAll(filepath.Dir(golden), 0o755)
				os.WriteFile(golden, append(got, '\n'), 0o644)
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(got)+"\n" {
				t.Fatalf("findings differ from %s:\n%s", golden, got)
			}
		})
	}
}

func TestParseProbeRejectsCutShortOutput(t *testing.T) {
	if _, err := ParseProbe("probe.version=1\nuid=0\nos.id=ubuntu\n"); err == nil {
		t.Fatal("output without probe.done must be an error")
	}
}

func TestProbeIsEmbedded(t *testing.T) {
	if !strings.Contains(Probe, "kv probe.done 1") || strings.Contains(Probe, "{{.Labels}}\n") {
		t.Fatal("probe script missing or reporting raw labels")
	}
}

func TestFixesCarryTiers(t *testing.T) {
	f, _ := ParseProbe(readFile(t, "testdata/probes/synthetic-busy-port-no-docker.txt"))
	tiers := map[string]Tier{}
	for _, fd := range Check(f) {
		if (fd.Severity == NeedsFix) != (fd.Fix != nil) {
			t.Fatalf("%s: severity %s with fix %v", fd.Why, fd.Severity, fd.Fix)
		}
		if fd.Fix != nil {
			tiers[fd.Fix.ID] = fd.Fix.Tier
		}
	}
	if tiers["install-docker"] != Sensitive || tiers["swap-file"] != Routine || tiers["time-sync"] != Routine {
		t.Fatalf("tiers %v", tiers)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
