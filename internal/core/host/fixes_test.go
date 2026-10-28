package host

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEveryProposedFixHasAScript(t *testing.T) {
	probes, _ := filepath.Glob("testdata/probes/*.txt")
	for _, p := range probes {
		f, err := ParseProbe(readFile(t, p))
		if err != nil {
			t.Fatal(err)
		}
		if _, missing := Propose(Check(f)); len(missing) > 0 {
			t.Errorf("%s: fixes without a script: %v", p, missing)
		}
	}
}

func TestProposeGroupsOnlyRoutineFixes(t *testing.T) {
	fd := func(id string, tier Tier) Finding {
		return Finding{Why: "test." + id, Severity: NeedsFix, Fix: &Fix{ID: id, Tier: tier}}
	}
	saved := Scripts
	t.Cleanup(func() { Scripts = saved })
	Scripts = map[string]FixScript{"r1": {Apply: "a"}, "r2": {Apply: "a", Undo: "u"}, "s1": {Apply: "a"}, "d1": {Apply: "a"}, "d2": {Apply: "a"}}
	batches, missing := Propose([]Finding{fd("d1", Destructive), fd("r1", Routine), fd("s1", Sensitive), fd("r2", Routine), fd("d2", Destructive), fd("x", Routine)})
	if len(missing) != 1 || missing[0] != "x" {
		t.Fatalf("missing %v", missing)
	}
	if len(batches) != 4 || batches[0].Tier != Routine || len(batches[0].Fixes) != 2 {
		t.Fatalf("batches %+v", batches)
	}
	for _, b := range batches[1:] {
		if len(b.Fixes) != 1 {
			t.Fatalf("a %s fix was grouped: %+v", b.Tier, b)
		}
	}
	if !batches[0].Fixes[1].CanUndo || batches[0].Fixes[0].CanUndo {
		t.Fatal("undo availability wrong")
	}
}

func TestManifest(t *testing.T) {
	at := time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC)
	rec := RecordScript("swap-file", "apply", at)
	if !strings.Contains(rec, ManifestPath) || !strings.Contains(rec, `"fix":"swap-file","action":"apply","at":"2026-10-19T14:00:00Z"`) {
		t.Fatalf("record script %q", rec)
	}
	m := `{"fix":"swap-file","action":"apply","at":"x"}
{"fix":"install-docker","action":"apply","at":"x"}
not json
{"fix":"swap-file","action":"undo","at":"x"}
{"fix":"time-sync","action":"apply","at":"x"}`
	if got := strings.Join(Applied(m), ","); got != "install-docker,time-sync" {
		t.Fatalf("applied %q", got)
	}
}

func TestFixScriptsRefuseWhatOAPDidntCreate(t *testing.T) {
	if !strings.Contains(Scripts["swap-file"].Apply, "already exists") || !strings.Contains(Scripts["install-docker"].Apply, "already running") {
		t.Fatal("fix scripts must refuse to overwrite existing swap or Docker")
	}
	if Scripts["install-docker"].Undo != "" {
		t.Fatal("installing Docker has no undo")
	}
}
