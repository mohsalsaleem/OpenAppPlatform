package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func fakeClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, e := New(domain.Target{ID: "fixture", Environment: "staging", URL: "unix:///var/run/docker.sock"})
	if e != nil {
		t.Fatal(e)
	}
	c.http = srv.Client()
	c.base = srv.URL
	return c
}
func TestLogsDemultiplexAndBounds(t *testing.T) {
	frame := func(stream byte, text string) []byte {
		b := make([]byte, 8+len(text))
		b[0] = stream
		binary.BigEndian.PutUint32(b[4:8], uint32(len(text)))
		copy(b[8:], text)
		return b
	}
	b := append(frame(1, "hello\n"), frame(2, "warning\n")...)
	got, e := demultiplex(b)
	if e != nil || got != "hello\nwarning\n" {
		t.Fatal(got, e)
	}
	for _, b := range [][]byte{{1}, {1, 0, 0, 0, 255, 255, 255, 255}, {3, 0, 0, 0, 0, 0, 0, 0}} {
		if _, e := demultiplex(b); e == nil {
			t.Fatal("invalid frame accepted")
		}
	}
}
func TestDockerRejectsRemoteUnauthenticatedEndpoints(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:2375", "tcp://server:2375", "unix://server/socket", "unix:relative", "unix:///socket?token=secret"} {
		if _, e := New(domain.Target{URL: u}); e == nil {
			t.Fatal("unsupported endpoint accepted", u)
		}
	}
}
func TestEnsureNeverTouchesForeignContainer(t *testing.T) {
	mutations := 0
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		if strings.Contains(r.URL.Path, "/images/") {
			json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:cached"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": "foreign", "Config": map[string]any{"Labels": map[string]string{prefix + "target": "another"}}, "State": map[string]any{"Running": true}})
	})
	_, e := c.Ensure(context.Background(), operator.Spec{Name: "same", Ownership: "OpenAppPlatform:" + strings.Repeat("a", 32) + ":web", Component: domain.Component{Name: "web", Image: "cached", Port: 8080}})
	if e == nil || mutations != 0 {
		t.Fatal("foreign container mutated")
	}
}
func TestMissingImageDoesNotPullInOfflineMode(t *testing.T) {
	calls := []string{}
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		http.NotFound(w, r)
	})
	_, e := c.image(context.Background(), "not-cached")
	if e == nil || len(calls) != 1 || !strings.HasPrefix(calls[0], "GET ") {
		t.Fatal("unexpected pull", calls, e)
	}
}
func TestPullStreamErrorIsNotSuccess(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{\"status\":\"pulling\"}\n{\"error\":\"private registry credentials\"}\n"))
	})
	e := c.pull(context.Background(), "image")
	if e == nil || strings.Contains(e.Error(), "credentials") {
		t.Fatal("pull error incorrectly handled", e)
	}
}
func TestSettingsAreStrict(t *testing.T) {
	_, e := New(domain.Target{URL: "unix:///var/run/docker.sock", Settings: json.RawMessage(`{"password":"secret"}`)})
	if e == nil {
		t.Fatal("unknown secret-bearing settings accepted")
	}
}

func TestRestartNeverMutatesForeignOrCandidateContainers(t *testing.T) {
	for _, reference := range []string{"foreign", "active-next"} {
		t.Run(reference, func(t *testing.T) {
			mutations := 0
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutations++
				}
				labels := map[string]string{prefix + "target": "fixture", prefix + "environment": "staging", prefix + "owner": "owner", prefix + "reference": "active"}
				if reference == "foreign" {
					labels[prefix+"target"] = "other"
				}
				json.NewEncoder(w).Encode(map[string]any{"Id": "container-id", "Config": map[string]any{"Labels": labels}})
			})
			if _, e := c.Restart(context.Background(), reference); e == nil || mutations != 0 {
				t.Fatal("foreign/candidate restart allowed")
			}
		})
	}
}
