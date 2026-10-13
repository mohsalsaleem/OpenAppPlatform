package ingress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConditionalPublicationReplayDriftAndUnknown(t *testing.T) {
	dir, err := os.MkdirTemp("", "oap-ingress-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	m, d, b, o, now := fixture()
	candidate, err := Compile("app", m, d, b, o, now)
	if err != nil {
		t.Fatal(err)
	}
	current := []byte(`{"admin":{"listen":"unix//run/oap-ingress/admin.sock"}}`)
	etag := "initial"
	writes := 0
	mode := "normal"
	var mu sync.Mutex
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "GET" {
			w.Header().Set("ETag", etag)
			w.Write(current)
			return
		}
		writes++
		if r.URL.Path != "/config/" || r.Header.Get("If-Match") != etag {
			w.WriteHeader(412)
			return
		}
		if mode == "race" {
			etag = "external"
			w.WriteHeader(412)
			return
		}
		if mode == "reject" {
			w.WriteHeader(400)
			w.Write([]byte("private-config-do-not-return"))
			return
		}
		current = candidate.JSON()
		etag = "applied"
		if mode == "lost" {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := New(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	writeCount := func() int { mu.Lock(); defer mu.Unlock(); return writes }
	expected, err := client.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mode = "race"
	mu.Unlock()
	if _, err = client.Apply(ctx, expected, candidate); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	expected, _ = client.Read(ctx)
	mu.Lock()
	mode = "reject"
	mu.Unlock()
	if _, err = client.Apply(ctx, expected, candidate); !errors.Is(err, ErrRejected) {
		t.Fatal(err)
	}
	mu.Lock()
	mode = "lost"
	mu.Unlock()
	receipt, err := client.Apply(ctx, expected, candidate)
	if !errors.Is(err, ErrUnknown) || receipt.State != "unknown" {
		t.Fatal(receipt, err)
	}
	mu.Lock()
	count := writes
	mu.Unlock()
	receipt, err = client.Apply(ctx, expected, candidate)
	if err != nil || receipt.State != "unchanged" || writeCount() != count {
		t.Fatal("replay mutated", receipt, err)
	}
	mu.Lock()
	current = []byte(`{"external":true}`)
	etag = "drift"
	mu.Unlock()
	if _, err = client.Apply(ctx, expected, candidate); !errors.Is(err, ErrConflict) || writeCount() != count {
		t.Fatal("drift overwritten", err)
	}
}
