package docker

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// fakeEngine serves the few Engine API calls that Observe and Events make,
// on a unix socket, so these tests run without Docker.
type fakeEngine struct {
	containers []map[string]any
	// health is State.Health per container id; a missing id means the
	// container has no health check (Docker reports null).
	health map[string]string
	events []map[string]any
}

func (f *fakeEngine) start(t *testing.T) string {
	t.Helper()
	// Unix socket paths are short on macOS, so not t.TempDir.
	dir, err := os.MkdirTemp("", "oapd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+APIVersion+"/info", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"Name": "vultr", "MemTotal": 950 << 20, "NCPU": 1})
	})
	mux.HandleFunc("GET /"+APIVersion+"/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(append([]map[string]any{}, f.containers...))
	})
	mux.HandleFunc("GET /"+APIVersion+"/containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		state := map[string]any{"Status": "running", "Health": nil}
		if h, ok := f.health[r.PathValue("id")]; ok {
			state["Health"] = map[string]any{"Status": h}
		}
		json.NewEncoder(w).Encode(map[string]any{"State": state})
	})
	mux.HandleFunc("GET /"+APIVersion+"/events", func(w http.ResponseWriter, _ *http.Request) {
		for _, e := range f.events {
			json.NewEncoder(w).Encode(e)
		}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return sock
}

// TestObserveAvailableMemory: available memory comes from the host's
// meminfo, and is unknown (absent, not 0) when that can't be read. The
// Engine API reports only the total, so on vultr_oap the briefing showed
// 0 MiB available.
func TestObserveAvailableMemory(t *testing.T) {
	sock := (&fakeEngine{}).start(t)
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	os.WriteFile(meminfo, []byte("MemTotal:         972800 kB\nMemFree:           61440 kB\nMemAvailable:     419840 kB\n"), 0o600)
	for _, c := range []struct {
		name, path string
		want       string
	}{
		{"meminfo", meminfo, "410"},
		{"no meminfo", filepath.Join(t.TempDir(), "missing"), "unknown"},
		{"not configured", "", "unknown"},
	} {
		r, err := New(sock, "test")
		if err != nil {
			t.Fatal(err)
		}
		r.MemInfo = c.path
		obs, err := r.Observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := available(obs.Host); got != c.want {
			t.Errorf("%s: available memory %s, want %s", c.name, got, c.want)
		}
		if obs.Host.MemoryTotalMiB != 950 {
			t.Errorf("%s: total memory %d MiB, want 950", c.name, obs.Host.MemoryTotalMiB)
		}
	}
}

func available(h model.HostObservation) string {
	if h.MemoryAvailableMiB == nil {
		return "unknown"
	}
	return fmt.Sprint(*h.MemoryAvailableMiB)
}

// TestImageWithoutHealthCheck: an instance whose image has no HEALTHCHECK
// is healthy while it runs, in Observe and in its start event alike. Docker
// never sends a health_status event for it, so a start event reported as
// "starting" would hold it there until the next resync.
func TestImageWithoutHealthCheck(t *testing.T) {
	labels := func(ordinal string) map[string]string {
		return map[string]string{LabelInstall: "test", LabelRole: RoleInstance, LabelApplication: "shop",
			LabelComponent: "worker", LabelOrdinal: ordinal, LabelRelease: "r-1"}
	}
	f := &fakeEngine{
		containers: []map[string]any{
			{"Id": "plain", "State": "running", "Status": "Up 2 seconds", "Labels": labels("1")},
			{"Id": "checked", "State": "running", "Status": "Up 2 seconds (health: starting)", "Labels": labels("2")},
		},
		health: map[string]string{"checked": "starting"},
		events: []map[string]any{
			{"Action": "start", "Actor": map[string]any{"ID": "plain", "Attributes": labels("1")}},
			{"Action": "start", "Actor": map[string]any{"ID": "checked", "Attributes": labels("2")}},
		},
	}
	r, err := New(f.start(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]model.InstanceHealth{"plain": model.HealthHealthy, "checked": model.HealthStarting}

	obs, err := r.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Instances) != 2 {
		t.Fatalf("observed %d instances, want 2", len(obs.Instances))
	}
	for _, in := range obs.Instances {
		if in.Health != want[in.ID] {
			t.Errorf("observe: %s is %v, want %v", in.ID, in.Health, want[in.ID])
		}
	}

	events, err := r.Events(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for e := range events {
		seen = append(seen, e.ID)
		if e.Kind != ops.EventStarted {
			t.Errorf("event for %s has kind %v, want started", e.ID, e.Kind)
		}
		if e.Health != want[e.ID] {
			t.Errorf("start event: %s is %v, want %v", e.ID, e.Health, want[e.ID])
		}
	}
	if strings.Join(seen, ",") != "plain,checked" {
		t.Errorf("events for %v, want plain and checked", seen)
	}
}
