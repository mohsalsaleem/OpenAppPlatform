package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
)

var ctx = context.Background()

func newServer(t *testing.T) *Server {
	t.Helper()
	clock := sim.NewClock(time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC))
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{"agents": {"owner": {"scope": {"applications": ["*"]}, "may": ["scale", "spec-patch"]}}}`))
	desired.SetEnvelope(context.Background(), env, "owner", "test envelope")
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return &Server{Service: svc, Caller: service.Owner, WatchIdle: 200 * time.Millisecond}
}

func rpc(t *testing.T, s *Server, method string, params any) map[string]any {
	t.Helper()
	p, _ := json.Marshal(params)
	out := s.Handle(ctx, []byte(`{"jsonrpc":"2.0","id":7,"method":"`+method+`","params":`+string(p)+`}`))
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%s: %v %s", method, err, out)
	}
	if res["id"] != float64(7) {
		t.Fatalf("%s: id %v", method, res["id"])
	}
	return res
}

// call runs a tool and returns its structured content and error flag.
func call(t *testing.T, s *Server, name string, args any) (map[string]any, bool) {
	t.Helper()
	res := rpc(t, s, "tools/call", map[string]any{"name": name, "arguments": args})
	r := res["result"].(map[string]any)
	sc, _ := r["structuredContent"].(map[string]any)
	return sc, r["isError"] == true
}

func TestInitializeAndTools(t *testing.T) {
	s := newServer(t)
	init := rpc(t, s, "initialize", map[string]any{"protocolVersion": ProtocolVersion})
	if init["result"].(map[string]any)["protocolVersion"] != ProtocolVersion {
		t.Fatalf("initialize %v", init)
	}
	if out := s.Handle(ctx, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); out != nil {
		t.Fatal("a notification got a response")
	}
	list := rpc(t, s, "tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	names := map[string]map[string]any{}
	for _, tl := range list {
		m := tl.(map[string]any)
		names[m["name"].(string)] = m
		if m["description"] == "" || m["inputSchema"] == nil {
			t.Fatalf("tool %v lacks a description or schema", m["name"])
		}
	}
	for _, want := range []string{"observe", "watch", "propose", "apply", "await", "explain", "ask", "login"} {
		if names[want] == nil {
			t.Fatalf("tool %s missing from %v", want, names)
		}
	}
	schema, _ := json.Marshal(names["propose"]["inputSchema"])
	for _, want := range []string{`"intent"`, `"spec_patch"`, `"merge_patch":{"type":"object"}`, `"expected_revision":{"type":["string","integer"]}`, `"$defs"`} {
		if !strings.Contains(string(schema), want) {
			t.Fatalf("propose schema lacks %s:\n%s", want, schema)
		}
	}
	if res := rpc(t, s, "nope", nil); res["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method: %v", res)
	}
}

func TestToolsCallTheService(t *testing.T) {
	s := newServer(t)
	p, isErr := call(t, s, "propose", map[string]any{"intent": map[string]any{"application": "site", "because": "create site",
		"spec_patch": map[string]any{"merge_patch": map[string]any{"environment": "production",
			"ingress":    map[string]any{"domain": "site.example.com", "routes": map[string]any{"/": map[string]any{"component": "web"}}},
			"components": map[string]any{"web": map[string]any{"kind": "web", "image": "reg/site:1", "port": 8080}}}}}})
	if isErr {
		t.Fatalf("propose: %v", p)
	}
	id := p["proposal"].(map[string]any)["id"].(string)
	a, isErr := call(t, s, "apply", map[string]any{"proposal_id": id})
	if isErr {
		t.Fatalf("apply: %v", a)
	}
	evs, _ := call(t, s, "await", map[string]any{"operation_id": a["operation_id"]})
	list := evs["events"].([]any)
	last := list[len(list)-1].(map[string]any)
	if last["outcome"].(map[string]any)["status"] != "OUTCOME_STATUS_ACHIEVED" {
		t.Fatalf("await: %v", last)
	}
	s.Service.Observer.Resync(ctx)
	w, _ := call(t, s, "observe", map[string]any{"application": "site"})
	if !strings.Contains(fmtJSON(w), `"converged":true`) || !strings.Contains(fmtJSON(w), `"spec_hash"`) {
		t.Fatalf("observe: %s", fmtJSON(w))
	}
	watched, _ := call(t, s, "watch", map[string]any{"since_revision": "1"})
	if n := len(watched["events"].([]any)); n < 5 {
		t.Fatalf("watch returned %d events", n)
	}
	e, isErr := call(t, s, "observe", map[string]any{"application": "nope"})
	if !isErr || e["why"] != service.WhyUnknownApp {
		t.Fatalf("typed error: %v %v", e, isErr)
	}
	if _, isErr := call(t, s, "observe", map[string]any{"bogus": 1}); !isErr {
		t.Fatal("unknown argument accepted")
	}
}

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestTransports(t *testing.T) {
	s := newServer(t)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.ServeStdio(ctx, in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"id":1`) || !strings.Contains(lines[1], `"tools"`) {
		t.Fatalf("stdio: %q", lines)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"id":3`) {
		t.Fatalf("http: %d %s", resp.StatusCode, b)
	}
	if resp, _ := http.Get(ts.URL); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
	_ = oapv1.File_oap_v1_agent_proto
}
