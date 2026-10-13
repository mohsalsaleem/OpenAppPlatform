package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runTest(t *testing.T, args []string, token, input string) (Result, int) {
	t.Helper()
	var out bytes.Buffer
	code := Run(context.Background(), args, func(k string) string {
		if k == "OAP_API_TOKEN" {
			return token
		}
		return ""
	}, strings.NewReader(input), &out)
	var result Result
	if json.Unmarshal(out.Bytes(), &result) != nil {
		t.Fatal("invalid envelope")
	}
	if strings.Contains(out.String(), token) && token != "" {
		t.Fatal("credential leaked")
	}
	return result, code
}
func TestCLIAdmissionRetryAndContext(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-token" {
			t.Error("identity missing")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			w.Write([]byte(`{"id":"app1","manifest":{"targetId":"docker","environment":"staging"}}`))
			return
		}
		writes++
		if r.Header.Get("Idempotency-Key") != "release-one" {
			t.Error("key lost")
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"id":"operation1","state":"queued"}`))
	}))
	defer server.Close()
	args := []string{"--url", server.URL, "--app", "app1", "--target", "docker", "--environment", "staging", "--key", "release-one", "--file", "-", "deploy"}
	for range 2 {
		result, code := runTest(t, args, "private-test-token", `{"expectedVersion":1}`)
		if code != 0 || result.OperationID != "operation1" || result.IdempotencyKey != "release-one" {
			t.Fatal(result, code)
		}
	}
	args[5] = "wrong-target"
	_, code := runTest(t, args, "private-test-token", `{"expectedVersion":1}`)
	if code != 2 || writes != 2 {
		t.Fatal("context did not block mutation")
	}
}
func TestCLIInputRejectedBeforeDispatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	for _, input := range []string{`{}`, `{"expectedVersion":0}`, `{} {}`, strings.Repeat("a", 65537)} {
		_, code := runTest(t, []string{"--url", server.URL, "--app", "app1", "--file", "-", "deploy"}, "private-test-token", input)
		if code != 2 {
			t.Fatal(code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid mutation dispatched")
	}
}
func TestCLIDenialsAreStructuredAndDoNotEchoProviderText(t *testing.T) {
	for _, status := range []int{401, 403, 404, 409, 429, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				w.Write([]byte(`{"message":"private-test-token sensitive provider input"}`))
			}))
			defer server.Close()
			result, code := runTest(t, []string{"--url", server.URL, "meta"}, "private-test-token", "")
			if result.Error == nil || result.Error.HTTPStatus != status || len(result.Error.NextActions) == 0 || code == 0 {
				t.Fatal("unstructured denial")
			}
		})
	}
}
func TestCLIWaitStatesAndTimeout(t *testing.T) {
	for _, state := range []string{"succeeded", "attention", "failed", "cancelled", "abandoned"} {
		t.Run(state, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]string{"id": "operation1", "state": state})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			raw, f := (Client{URL: server.URL, Token: "private-test-token"}).Wait(ctx, "operation1", time.Millisecond)
			if len(raw) == 0 {
				t.Fatal("lost last observation")
			}
			if state == "succeeded" {
				if f != nil {
					t.Fatal(f)
				}
			} else if f == nil || (state == "queued" && f.Code != "wait_timeout") {
				t.Fatal("incorrect outcome")
			}
		})
	}
}
func TestCLIRejectsRedirectAndOversize(t *testing.T) {
	leaked := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer server.Close()
	_, f := (Client{URL: server.URL, Token: "private-test-token"}).Call(context.Background(), "GET", "/api/v1/meta", nil, "")
	if f == nil || leaked {
		t.Fatal("redirect followed")
	}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat(" ", maxBody+1))) }))
	defer big.Close()
	_, f = (Client{URL: big.URL, Token: "private-test-token"}).Call(context.Background(), "GET", "/api/v1/meta", nil, "")
	if f == nil || f.Code != "invalid_response" {
		t.Fatal("unbounded response")
	}
}
func TestCLIOwnerSessionFileAndSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	os.WriteFile(path, []byte(`{"session":"owner-private"}`), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("oap-session")
		if e != nil || c.Value != "owner-private" || r.Header.Get("X-OAP-CSRF") != "1" || r.Header.Get("Authorization") != "" {
			t.Error("owner identity incorrect")
		}
		w.Write([]byte(`{"id":"app1"}`))
	}))
	defer server.Close()
	result, code := runTest(t, []string{"--url", server.URL, "--session-file", path, "--file", "-", "create"}, "", `{}`)
	if code != 0 || !result.OK {
		t.Fatal("session failed")
	}
	os.Chmod(path, 0644)
	_, code = runTest(t, []string{"--session-file", path, "meta"}, "", "")
	if code != 2 {
		t.Fatal("public session file accepted")
	}
	_, code = runTest(t, []string{"schema"}, "", "")
	if code != 0 {
		t.Fatal("offline schema failed")
	}
}
func TestCLIVerifyIsBoundedCurrentHealth(t *testing.T) {
	for _, status := range []string{"running:healthy", "running", "running:starting", "running:unhealthy", "unavailable"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"status": status, "retired": false}})
		}))
		_, code := runTest(t, []string{"--url", server.URL, "--app", "app1", "verify"}, "private-test-token", "")
		server.Close()
		if (status == "running" || status == "running:healthy") != (code == 0) {
			t.Fatal(status, code)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCLIWaitPreservesLastObservationOnInterruptedPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := Client{URL: "http://127.0.0.1", Token: "private-test-token", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 2 {
			cancel()
			return nil, context.Canceled
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"operation1","state":"queued"}`)), Header: make(http.Header)}, nil
	})}}
	raw, f := client.Wait(ctx, "operation1", time.Nanosecond)
	if f == nil || f.Code != "wait_timeout" || !strings.Contains(string(raw), "queued") || calls != 2 {
		t.Fatal("lost last observation during interruption")
	}
}
