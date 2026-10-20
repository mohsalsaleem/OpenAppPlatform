package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

const testPub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOAPbootstrapkeyfortests oap-bootstrap 2026-10-19 laptop"

// handoffHarness is a server at the given stage with OAP's key on this
// machine, and an oapd (simulated) whose public listener the owner key
// signs in to.
func handoffHarness(t *testing.T, terminal bool, input string, stage host.Stage, enroll bool) *harness {
	t.Helper()
	local, public := twoListeners(t)
	h := newHarness(t, terminal, input, "")
	h.a.now = time.Now
	if err := h.a.update(host.Server{Address: "root@203.0.113.5", Stage: stage, Install: "a1b2c3d4e5", API: public}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.a.dir(), "hosts", "203.0.113.5")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "bootstrap_ed25519.pub"), []byte(testPub+"\n"), 0o600)
	t.Setenv("OAP_AGENT", "")
	if enroll {
		t.Setenv("OAP_AGENT_URL", local)
		if code := h.run("owner", "enroll", "--label", "test"); code != 0 {
			t.Fatalf("enroll: %s", h.out.String())
		}
		h.out.Reset()
	}
	t.Setenv("OAP_AGENT_URL", "")
	return h
}

func stage(t *testing.T, h *harness) host.Stage {
	t.Helper()
	s, err := h.a.server("")
	if err != nil {
		t.Fatal(err)
	}
	return s.Stage
}

// TestHandoff: hand-off refuses without a working owner key; with one it
// records the removal, removes the key and proves the server refuses it;
// if the key still works, it says so and doesn't move on.
func TestHandoff(t *testing.T) {
	t.Run("no owner key", func(t *testing.T) {
		h := handoffHarness(t, false, "", host.StageInstalled, false)
		if code := h.run("host", "handoff"); code == 0 || !strings.Contains(h.out.String(), "owner enroll") {
			t.Fatalf("hand-off without an owner key: %d %s", code, h.out.String())
		}
		if len(h.ssh.scripts) != 0 || stage(t, h) != host.StageInstalled {
			t.Fatal("hand-off without an owner key touched the server")
		}
	})
	t.Run("removes and proves", func(t *testing.T) {
		h := handoffHarness(t, false, "", host.StageOwned, true)
		if code := h.run("host", "handoff"); code != 0 {
			t.Fatalf("hand-off: %s", h.out.String())
		}
		ran := h.ssh.ran(t)
		if len(ran) != 2 {
			t.Fatalf("want the removal and the check, got %d scripts", len(ran))
		}
		rec, rm := strings.Index(ran[0], `"fix":"bootstrap-key","action":"undo"`), strings.Index(ran[0], "grep -vF ' AAAAC3NzaC1lZDI1NTE5AAAAIOAPbootstrapkeyfortests'")
		if rec < 0 || rm < 0 || rec > rm {
			t.Fatalf("the removal isn't recorded before it happens:\n%s", ran[0])
		}
		var out map[string]any
		if err := json.Unmarshal(h.out.Bytes(), &out); err != nil || out["key_refused"] != true {
			t.Fatalf("output: %s", h.out.String())
		}
		if stage(t, h) != host.StageHandedOff {
			t.Fatal("not handed off")
		}
		// From here owner commands work without SSH.
		h.out.Reset()
		if code := h.run("owner", "keys"); code != 0 {
			t.Fatalf("owner after hand-off: %s", h.out.String())
		}
	})
	t.Run("key still works", func(t *testing.T) {
		h := handoffHarness(t, false, "", host.StageOwned, true)
		h.ssh.keyElsewhere = true
		if code := h.run("host", "handoff"); code == 0 || !strings.Contains(h.out.String(), "still works") {
			t.Fatalf("hand-off with the key still working: %d %s", code, h.out.String())
		}
		if stage(t, h) != host.StageOwned {
			t.Fatal("moved on although the key still works")
		}
	})
}

const uninstallManifest = `{"fix":"install-docker","action":"apply","at":"2026-10-19T10:00:00Z"}
{"fix":"swap-file","action":"apply","at":"2026-10-19T10:01:00Z"}
{"fix":"install-oap","action":"apply","at":"2026-10-19T10:03:00Z"}
`

// TestUninstall: without a terminal uninstall only shows its plan for the
// owner; on a terminal it removes OAP after the owner types the address,
// undoes each fix the owner approves, keeps what can't be undone, and
// removes OAP's key; a wrong address removes nothing.
func TestUninstall(t *testing.T) {
	t.Run("agent sees the plan", func(t *testing.T) {
		h := handoffHarness(t, false, "", host.StageInstalled, false)
		h.ssh.manifest = uninstallManifest
		if code := h.run("host", "uninstall"); code != host.ExitOwner {
			t.Fatalf("exit %d: %s", code, h.out.String())
		}
		var plan struct {
			Steps []host.UninstallStep `json:"steps"`
		}
		json.Unmarshal(h.out.Bytes(), &plan)
		if len(plan.Steps) != 3 || len(h.ssh.scripts) != 1 {
			t.Fatalf("plan %+v after %d scripts", plan.Steps, len(h.ssh.scripts))
		}
	})
	t.Run("wrong address", func(t *testing.T) {
		h := handoffHarness(t, true, "203.0.113.5\n", host.StageInstalled, false)
		h.ssh.manifest = uninstallManifest
		if code := h.run("host", "uninstall"); code != host.ExitOwner || len(h.ssh.scripts) != 1 {
			t.Fatalf("exit %d after %d scripts: %s", code, len(h.ssh.scripts), h.out.String())
		}
	})
	t.Run("owner approves", func(t *testing.T) {
		h := handoffHarness(t, true, "root@203.0.113.5\ny\n", host.StageInstalled, true)
		h.ssh.manifest = uninstallManifest
		if code := h.run("host", "uninstall"); code != 0 {
			t.Fatalf("uninstall: %s", h.out.String())
		}
		ran := h.ssh.ran(t)
		// manifest read, OAP removed, swap undone, key removed, key refused.
		if len(ran) != 5 {
			t.Fatalf("%d scripts:\n%s", len(ran), strings.Join(ran, "\n-----\n"))
		}
		if !strings.Contains(ran[1], "ID=a1b2c3d4e5") || !strings.Contains(ran[1], `"fix":"install-oap","action":"undo"`) {
			t.Fatalf("OAP's removal:\n%s", ran[1])
		}
		if !strings.Contains(ran[2], "swapoff /swapfile") || !strings.Contains(ran[2], `"fix":"swap-file","action":"undo"`) {
			t.Fatalf("swap undo:\n%s", ran[2])
		}
		if !strings.Contains(ran[3], "n=$(grep -cF") || strings.Contains(ran[3], "rm -rf /var/lib/oap") {
			t.Fatalf("key removal:\n%s", ran[3])
		}
		for _, sc := range ran {
			if strings.Contains(sc, "rm -rf /var/lib/oap") {
				t.Fatal("OAP's directory was removed although Docker, which has no undo, stays")
			}
		}
		if !strings.Contains(h.out.String(), "Keeping install-docker") || stage(t, h) != host.StageNew {
			t.Fatalf("output: %s", h.out.String())
		}
		if _, err := os.Stat(h.a.ownerDir(&host.Server{Address: "root@203.0.113.5"})); !os.IsNotExist(err) {
			t.Fatal("the owner key for an uninstalled server is still on this machine")
		}
	})
	t.Run("nothing kept", func(t *testing.T) {
		h := handoffHarness(t, true, "root@203.0.113.5\n", host.StageInstalled, false)
		h.ssh.manifest = `{"fix":"install-oap","action":"apply","at":"2026-10-19T10:03:00Z"}` + "\n"
		if code := h.run("host", "uninstall", "--keep-access"); code != 0 {
			t.Fatalf("uninstall: %s", h.out.String())
		}
		ran := h.ssh.ran(t)
		if len(ran) != 3 || !strings.Contains(ran[2], "rm -rf /var/lib/oap") || stage(t, h) != host.StageConnected {
			t.Fatalf("%d scripts, stage %s:\n%s", len(ran), stage(t, h), strings.Join(ran, "\n-----\n"))
		}
	})
}
