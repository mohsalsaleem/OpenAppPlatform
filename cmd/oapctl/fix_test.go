package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

const freshUbuntu = probeFixture + "vultr-oap-ubuntu-26-04.txt"

// checked returns a harness whose journey has the server checked against
// the given probe fixture.
func checked(t *testing.T, terminal bool, input, probe string) *harness {
	h := newHarness(t, terminal, input, probe)
	h.a.update(host.Server{Address: "root@203.0.113.5", Stage: host.StageChecked, Findings: findings(t, strings.TrimPrefix(probe, probeFixture))})
	return h
}

func applied(h *harness, t *testing.T, fix string) bool {
	for _, s := range h.ssh.ran(t) {
		if s == host.Scripts[fix].Apply {
			return true
		}
	}
	return false
}

// TestFixWithoutTerminalOnlyProposes: an agent sees the proposal with the
// exact commands, and nothing runs.
func TestFixWithoutTerminalOnlyProposes(t *testing.T) {
	h := checked(t, false, "", freshUbuntu)
	if code := h.run("host", "fix"); code != host.ExitOwner {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	var p fixProposal
	if err := json.Unmarshal(h.out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Batches) != 1 || p.Batches[0].Fixes[0].ID != "install-docker" || p.Batches[0].Fixes[0].Script == "" || p.AskOwner == "" {
		t.Fatalf("proposal %+v", p)
	}
	if len(h.ssh.scripts) != 0 {
		t.Fatalf("%d scripts ran without the owner", len(h.ssh.scripts))
	}
}

func TestUnapprovedFixNeverRuns(t *testing.T) {
	h := checked(t, true, "n\n", freshUbuntu)
	h.run("host", "fix")
	if applied(h, t, "install-docker") || !strings.Contains(h.out.String(), "Not applied.") {
		t.Fatalf("fix ran without approval\n%s", h.out.String())
	}
	for _, s := range h.ssh.ran(t) {
		if strings.Contains(s, host.ManifestPath) {
			t.Fatal("manifest written for an unapproved fix")
		}
	}
}

// TestApprovedFixRunsAndIsRecorded: the fix runs, the manifest records
// it, and the server is checked again.
func TestApprovedFixRunsAndIsRecorded(t *testing.T) {
	h := checked(t, true, "y\n", freshUbuntu)
	h.run("host", "fix")
	ran := h.ssh.ran(t)
	if len(ran) < 3 || ran[0] != host.Scripts["install-docker"].Apply ||
		!strings.Contains(ran[1], `"fix":"install-docker","action":"apply"`) || !strings.Contains(ran[2], "kv probe.done") {
		t.Fatalf("scripts run: %d\n%s", len(ran), h.out.String())
	}
	golden(t, "fix-install-docker.txt", h.out.String())
}

// TestRoutineFixesAreApprovedTogether: one answer covers the routine
// batch; the sensitive fix is asked separately.
func TestRoutineFixesAreApprovedTogether(t *testing.T) {
	h := checked(t, true, "y\nn\n", probeFixture+"synthetic-busy-port-no-docker.txt")
	h.run("host", "fix")
	out := h.out.String()
	if !strings.Contains(out, "Apply these 2 routine fixes? [y/N]") || !strings.Contains(out, "Apply this? [y/N]") {
		t.Fatalf("prompts:\n%s", out)
	}
	if !applied(h, t, "swap-file") || !applied(h, t, "time-sync") || applied(h, t, "install-docker") {
		t.Fatal("wrong fixes ran")
	}
}

func TestUndoNeedsManifestAndApproval(t *testing.T) {
	h := checked(t, true, "y\n", freshUbuntu)
	if code := h.run("host", "undo", "swap-file"); code != 1 || !strings.Contains(h.out.String(), "doesn't show swap-file") {
		t.Fatalf("undo without a manifest entry: exit %d\n%s", code, h.out.String())
	}
	if code := h.run("host", "undo", "install-docker"); code != 1 {
		t.Fatalf("install-docker has no undo, exit %d", code)
	}
	h = checked(t, true, "y\n", freshUbuntu)
	h.ssh.manifest = `{"fix":"swap-file","action":"apply","at":"2026-10-19T14:00:00Z"}` + "\n"
	h.run("host", "undo", "swap-file")
	ran := h.ssh.ran(t)
	found := false
	for _, s := range ran {
		found = found || s == host.Scripts["swap-file"].Undo
	}
	if !found || !strings.Contains(strings.Join(ran, "\n"), `"action":"undo"`) {
		t.Fatalf("undo didn't run or wasn't recorded\n%s", h.out.String())
	}
}

// TestCheckKeepsProgress: checking an installed server again keeps its
// installation id, and its stage when nothing blocks.
func TestCheckKeepsProgress(t *testing.T) {
	h := newHarness(t, false, "", probeFixture+"synthetic-ufw-web-open.txt")
	h.a.update(host.Server{Address: "root@203.0.113.5", Stage: host.StageInstalled, Install: "abc123def0"})
	h.run("host", "check")
	s, _ := h.a.server("root@203.0.113.5")
	if s.Stage != host.StageInstalled || s.Install != "abc123def0" {
		t.Fatalf("after check: %+v", s)
	}
	h = newHarness(t, false, "", freshUbuntu)
	h.a.update(host.Server{Address: "root@203.0.113.5", Stage: host.StageInstalled, Install: "abc123def0"})
	h.run("host", "check")
	s, _ = h.a.server("root@203.0.113.5")
	if s.Stage != host.StageChecked || s.Install != "abc123def0" {
		t.Fatalf("a blocking finding should need fixing again, keeping the id: %+v", s)
	}
}
