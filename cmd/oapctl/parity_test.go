package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/desiredyaml"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/mcp"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/wire"
	"google.golang.org/protobuf/types/known/structpb"
)

const parityYAML = `api_version: oap.dev/v1
kind: Application
name: site
environment: production
components:
  web:
    kind: web
    image: reg/site:1
    port: 8080
ingress:
  domain: site.example.com
  routes:
    /:
      component: web
`

// stack is a fresh simulated server with the agent API and MCP on one
// cleartext HTTP/2 listener, and its journal.
func stack(t *testing.T) (*httptest.Server, *sim.Journal, *http.Client) {
	t.Helper()
	clock := sim.NewClock(time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC))
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{"agents": {"owner": {"scope": {"applications": ["*"]}, "may": ["scale", "spec-patch"]}}}`))
	desired.SetEnvelope(env)
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle((&agentapi.Server{Service: svc, Caller: service.Owner}).Handler())
	mux.Handle("/mcp", &mcp.Server{Service: svc, Caller: service.Owner})
	ts := httptest.NewUnstartedServer(mux)
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	ts.Config.Protocols = &p
	ts.Start()
	t.Cleanup(ts.Close)
	var cp http.Protocols
	cp.SetUnencryptedHTTP2(true)
	return ts, journal, &http.Client{Transport: &http.Transport{Protocols: &cp}}
}

// intents are the scenario, built as the CLI builds them.
func intents(t *testing.T) []*oapv1.Intent {
	tree, probs := desiredyaml.Parse([]byte(parityYAML))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	patch := spec.Clone(tree).(map[string]any)
	for _, k := range []string{"api_version", "kind", "name"} {
		delete(patch, k)
	}
	st, err := structpb.NewStruct(patch)
	if err != nil {
		t.Fatal(err)
	}
	return []*oapv1.Intent{
		{Application: "site", Because: "apply oap.yaml", Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: st}}},
		{Application: "site", Because: "scale web to 2", Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "web", Instances: 2}}},
	}
}

func viaClient(t *testing.T, c oapv1connect.AgentServiceClient) {
	ctx := context.Background()
	for _, in := range intents(t) {
		p, err := c.Propose(ctx, connect.NewRequest(&oapv1.ProposeRequest{Intent: in}))
		if err != nil {
			t.Fatal(err)
		}
		a, err := c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: p.Msg.Proposal.Id}))
		if err != nil {
			t.Fatal(err)
		}
		stream, err := c.Await(ctx, connect.NewRequest(&oapv1.AwaitRequest{OperationId: a.Msg.OperationId}))
		if err != nil {
			t.Fatal(err)
		}
		for stream.Receive() {
		}
		if err := stream.Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func viaMCP(t *testing.T, url string) {
	call := func(name string, args any) map[string]any {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args}})
		resp, err := http.Post(url+"/mcp", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var res struct {
			Result struct {
				StructuredContent map[string]any `json:"structuredContent"`
				IsError           bool           `json:"isError"`
			} `json:"result"`
		}
		json.Unmarshal(b, &res)
		if res.Result.IsError {
			t.Fatalf("%s: %s", name, b)
		}
		return res.Result.StructuredContent
	}
	for _, in := range intents(t) {
		args, _ := wireJSON(in)
		p := call("propose", map[string]any{"intent": args})
		a := call("apply", map[string]any{"proposal_id": p["proposal"].(map[string]any)["id"]})
		call("await", map[string]any{"operation_id": a["operation_id"]})
	}
}

func viaCLI(t *testing.T, url string) {
	t.Setenv("OAP_AGENT_URL", url)
	h := newHarness(t, false, "", "")
	file := filepath.Join(t.TempDir(), "oap.yaml")
	os.WriteFile(file, []byte(parityYAML), 0o600)
	if code := h.run("app", "apply", file, "--yes"); code != 0 {
		t.Fatalf("app apply: %d\n%s", code, h.out.String())
	}
	if code := h.run("app", "scale", "site", "web", "2", "--yes"); code != 0 {
		t.Fatalf("app scale: %d\n%s", code, h.out.String())
	}
}

// journalText is a journal as comparable text: every entry, every field.
func journalText(t *testing.T, j *sim.Journal) string {
	entries, err := j.Since(context.Background(), 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%d %s %s %s %d %s %s\n", e.Revision, e.At.Format(time.RFC3339Nano), e.Kind, e.Operation, e.Step, e.State, e.Data)
	}
	return b.String()
}

// TestParityAcrossInterfaces: one scenario through Connect JSON, gRPC, MCP
// and the CLI leaves identical journals.
func TestParityAcrossInterfaces(t *testing.T) {
	journals := map[string]string{}
	for _, path := range []string{"connect-json", "grpc", "mcp", "cli"} {
		ts, journal, h2 := stack(t)
		switch path {
		case "connect-json":
			viaClient(t, oapv1connect.NewAgentServiceClient(http.DefaultClient, ts.URL, connect.WithProtoJSON()))
		case "grpc":
			viaClient(t, oapv1connect.NewAgentServiceClient(h2, ts.URL, connect.WithGRPC()))
		case "mcp":
			viaMCP(t, ts.URL)
		case "cli":
			viaCLI(t, ts.URL)
		}
		journals[path] = journalText(t, journal)
	}
	want := journals["connect-json"]
	if strings.Count(want, "\n") < 15 || !strings.Contains(want, " outcome ") {
		t.Fatalf("scenario journal too short:\n%s", want)
	}
	for path, got := range journals {
		if got != want {
			t.Fatalf("%s's journal differs from Connect JSON's:\n--- connect-json\n%s\n--- %s\n%s", path, want, path, got)
		}
	}
}

// wireJSON is an intent as MCP arguments: OAP's JSON as a generic value.
func wireJSON(in *oapv1.Intent) (any, error) {
	b, err := wire.Marshal(in)
	if err != nil {
		return nil, err
	}
	var v any
	return v, json.Unmarshal(b, &v)
}
