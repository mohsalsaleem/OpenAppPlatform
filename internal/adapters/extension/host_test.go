package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

var binaries = map[string]string{} // mode -> path

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "oap-testext-")
	if err != nil {
		panic(err)
	}
	for _, mode := range []string{"good", "crash", "hang", "hog", "liar"} {
		out := filepath.Join(dir, mode)
		cmd := exec.Command("go", "build", "-ldflags", "-X main.mode="+mode, "-o", out, "./testdata/testext")
		if b, err := cmd.CombinedOutput(); err != nil {
			panic(fmt.Sprintf("build %s: %v\n%s", mode, err, b))
		}
		binaries[mode] = out
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func digest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newHost(t *testing.T) *Host {
	t.Helper()
	h := &Host{SecretValue: func(name string) (string, error) { return "value-of-" + name, nil }}
	t.Cleanup(h.Close)
	return h
}

func install(t *testing.T, h *Host, name, mode string) {
	t.Helper()
	h.Add(Config{Name: name, Path: binaries[mode], SHA256: digest(t, binaries[mode]),
		Points: []extensionv1.Point{extensionv1.Point_POINT_NOTIFIER}, Settings: map[string]string{"channel": "ops"},
		Secrets: []string{"token"}, MemoryMiB: 64, CallTimeout: 2 * time.Second})
}

var note = ops.Notification{Kind: "ask", Subject: "s", Body: "b"}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// TestGrantsOnly: a well-behaved extension gets exactly its granted
// settings and secrets (not the secret it asked for but wasn't granted),
// and none of oapd's environment.
func TestGrantsOnly(t *testing.T) {
	t.Setenv("OAPD_WEBHOOK_SECRET", "canary-host-secret")
	h := newHost(t)
	h.Add(Config{Name: "testext", Path: binaries["good"], SHA256: digest(t, binaries["good"]),
		Points: []extensionv1.Point{extensionv1.Point_POINT_NOTIFIER}, Settings: map[string]string{"channel": "ops"},
		Secrets: []string{"token"}})
	detail, err := h.Notifier("testext").NotifyDetail(context.Background(), note)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Settings map[string]string `json:"settings"`
		Secrets  []string          `json:"secrets"`
		Env      []string          `json:"env"`
	}
	json.Unmarshal([]byte(detail), &got)
	if len(got.Settings) != 1 || got.Settings["channel"] != "ops" {
		t.Errorf("settings %v", got.Settings)
	}
	if strings.Join(got.Secrets, ",") != "token=value-of-token" {
		t.Errorf("secrets %v; want only the granted token", got.Secrets)
	}
	for _, k := range got.Env {
		if strings.HasPrefix(k, "OAPD_") || k == "HOME" || k == "USER" {
			t.Errorf("the extension inherited %s from the host", k)
		}
	}
	if err := h.Notifier("testext").Notify(context.Background(), note); err != nil {
		t.Fatal(err)
	}
}

// TestFailuresStayInTheExtension: a crashing, a hanging and a memory-hungry
// extension each fail their own calls, within bounds, and are stopped; a
// healthy extension keeps working throughout, and so does the host.
func TestFailuresStayInTheExtension(t *testing.T) {
	h := newHost(t)
	install(t, h, "testext", "good")
	healthy := func() {
		t.Helper()
		if err := h.Notifier("testext").Notify(context.Background(), note); err != nil {
			t.Fatalf("the healthy extension failed: %v", err)
		}
	}
	healthy()

	for _, c := range []struct {
		mode    string
		want    error
		within  time.Duration
		message string
	}{
		{"crash", nil, 5 * time.Second, "crashing"},
		{"hang", ErrTimeout, 2*time.Second + 3*time.Second, "hanging"},
		{"hog", ErrMemoryCap, 2*time.Second + 3*time.Second, "memory-hungry"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			name := "testext"
			// Each misbehaving binary claims to be testext; install it under
			// its own registration so the healthy one stays installed.
			h2 := newHost(t)
			install(t, h2, name, c.mode)
			// Start it first: the bound is on handling the failure, and a
			// fresh binary's first launch can take seconds on a busy machine.
			r, err := h2.get(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h2.start(context.Background(), r); err != nil {
				t.Fatalf("starting the %s extension: %v", c.message, err)
			}
			start := time.Now()
			err = h2.Notifier(name).Notify(context.Background(), note)
			took := time.Since(start)
			var ee *Error
			if err == nil || !errors.As(err, &ee) {
				t.Fatalf("the %s extension's call: %v", c.message, err)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("the %s extension's call: %v, want %v", c.message, err, c.want)
			}
			if c.want == nil && errors.Is(err, ErrTimeout) {
				t.Fatalf("the %s extension's call timed out instead of failing: %v", c.message, err)
			}
			if took > c.within {
				t.Fatalf("the %s extension's call took %s", c.message, took)
			}
			if pid := h2.PID(name); pid != 0 {
				t.Fatalf("the %s extension still runs as %d", c.message, pid)
			}
			healthy()
			// It is started afresh on the next use, and fails the same way.
			if err := h2.Notifier(name).Notify(context.Background(), note); err == nil {
				t.Fatalf("the %s extension succeeded on its second call", c.message)
			}
			healthy()
		})
	}
}

// TestRefusesMismatchedExtensions: a binary that isn't what was installed
// (changed after install, or claiming another name) never runs a call, and
// neither does a point that wasn't granted.
func TestRefusesMismatchedExtensions(t *testing.T) {
	h := newHost(t)
	h.Add(Config{Name: "testext", Path: binaries["good"], SHA256: strings.Repeat("0", 64),
		Points: []extensionv1.Point{extensionv1.Point_POINT_NOTIFIER}})
	if err := h.Notifier("testext").Notify(context.Background(), note); !errors.Is(err, ErrDigest) {
		t.Fatalf("changed binary: %v", err)
	}
	install(t, h, "testext", "liar")
	if err := h.Notifier("testext").Notify(context.Background(), note); err == nil || !strings.Contains(err.Error(), "someone-else") {
		t.Fatalf("another name: %v", err)
	}
	h.Add(Config{Name: "testext", Path: binaries["good"], SHA256: digest(t, binaries["good"])})
	if err := h.Notifier("testext").Notify(context.Background(), note); !errors.Is(err, ErrNotGranted) {
		t.Fatalf("ungranted point: %v", err)
	}
	h.Add(Config{Name: "testext", Path: binaries["good"], SHA256: digest(t, binaries["good"]),
		Points: []extensionv1.Point{extensionv1.Point_POINT_NOTIFIER}, Secrets: []string{"not-asked-for"}})
	if err := h.Notifier("testext").Notify(context.Background(), note); err == nil || !strings.Contains(err.Error(), "doesn't ask for it") {
		t.Fatalf("a grant it didn't ask for: %v", err)
	}
}

var _ ports.Extensions = (*Host)(nil)
