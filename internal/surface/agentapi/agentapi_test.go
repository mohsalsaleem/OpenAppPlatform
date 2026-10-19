package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var ctx = context.Background()

// server starts the agent API over HTTP/1.1 and cleartext HTTP/2 on a
// simulated host with one application deployed.
func server(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	clock := sim.NewClock(time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC))
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim", MemoryTotalMiB: 950}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{"agents": {"owner": {"scope": {"applications": ["*"]}, "may": ["scale", "spec-patch"]}}}`))
	desired.SetEnvelope(env)
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	patch, _ := structpb.NewStruct(map[string]any{"environment": "production",
		"ingress":    map[string]any{"domain": "site.example.com", "routes": map[string]any{"/": map[string]any{"component": "web"}}},
		"components": map[string]any{"web": map[string]any{"kind": "web", "image": "reg/site:1", "port": 8080}}})
	p, err := svc.Propose(ctx, service.Owner, &oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: "site",
		Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: patch}}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := svc.Apply(ctx, service.Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		t.Fatal(err)
	}
	svc.Await(ctx, service.Owner, &oapv1.AwaitRequest{OperationId: a.OperationId}, func(*oapv1.Event) error { return nil })
	svc.Observer.Resync(ctx)

	mux := http.NewServeMux()
	mux.Handle((&Server{Service: svc, Caller: service.Owner}).Handler())
	ts := httptest.NewUnstartedServer(mux)
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	ts.Config.Protocols = &protocols
	ts.Start()
	t.Cleanup(ts.Close)
	var cp http.Protocols
	cp.SetUnencryptedHTTP2(true)
	return ts, &http.Client{Transport: &http.Transport{Protocols: &cp}}
}

// TestSameResultOverEveryProtocol: Observe returns the identical world over
// gRPC, gRPC-Web, Connect and plain JSON, and an error carries the same
// code and typed detail over each.
func TestSameResultOverEveryProtocol(t *testing.T) {
	ts, h2 := server(t)
	clients := map[string]oapv1connect.AgentServiceClient{
		"grpc":     oapv1connect.NewAgentServiceClient(h2, ts.URL, connect.WithGRPC()),
		"grpc-web": oapv1connect.NewAgentServiceClient(h2, ts.URL, connect.WithGRPCWeb()),
		"connect":  oapv1connect.NewAgentServiceClient(http.DefaultClient, ts.URL),
	}
	var first *oapv1.ObserveResponse
	for name, c := range clients {
		res, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: "site", Detail: true}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(res.Msg.World.Applications) != 1 {
			t.Fatalf("%s: %v", name, res.Msg)
		}
		if first == nil {
			first = res.Msg
		} else if !proto.Equal(first, res.Msg) {
			t.Fatalf("%s differs:\n%v\n%v", name, res.Msg, first)
		}
		_, err = c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: "nope"}))
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Code() != connect.CodeNotFound || len(ce.Details()) != 1 {
			t.Fatalf("%s: error %v", name, err)
		}
		v, derr := ce.Details()[0].Value()
		if d, ok := v.(*oapv1.ErrorDetail); derr != nil || !ok || d.Why != service.WhyUnknownApp {
			t.Fatalf("%s: detail %v %v", name, v, derr)
		}
	}

	// Plain JSON, as curl sends it.
	body := []byte(`{"application": "site", "detail": true}`)
	resp, err := http.Post(ts.URL+oapv1connect.AgentServiceObserveProcedure, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var viaCurl oapv1.ObserveResponse
	if err := protojson.Unmarshal(raw, &viaCurl); err != nil || resp.StatusCode != 200 {
		t.Fatalf("curl: %d %s %v", resp.StatusCode, raw, err)
	}
	if !proto.Equal(first, &viaCurl) {
		t.Fatal("curl result differs")
	}
	// JSON follows OAP's conventions: field names as written, enums as strings.
	if !bytes.Contains(raw, []byte(`"memory_total_mib"`)) || bytes.Contains(raw, []byte(`"memoryTotalMib"`)) ||
		!bytes.Contains(raw, []byte(`"HEALTH_STATUS_HEALTHY"`)) {
		t.Fatalf("JSON conventions: %s", raw)
	}
	resp, _ = http.Post(ts.URL+oapv1connect.AgentServiceObserveProcedure, "application/json", bytes.NewReader([]byte(`{"application": "nope"}`)))
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var e struct {
		Code    string `json:"code"`
		Details []struct {
			Type  string `json:"type"`
			Debug struct {
				Why string `json:"why"`
			} `json:"debug"`
		} `json:"details"`
	}
	json.Unmarshal(raw, &e)
	if resp.StatusCode != 404 || e.Code != "not_found" || len(e.Details) != 1 || e.Details[0].Type != "oap.v1.ErrorDetail" {
		t.Fatalf("curl error: %d %s", resp.StatusCode, raw)
	}
}

// TestUnavailablePrimitivesSayWhy: watch and ask report Unimplemented,
// over a gRPC stream and a unary call.
func TestUnavailablePrimitivesSayWhy(t *testing.T) {
	ts, h2 := server(t)
	c := oapv1connect.NewAgentServiceClient(h2, ts.URL, connect.WithGRPC())
	stream, err := c.Watch(ctx, connect.NewRequest(&oapv1.WatchRequest{}))
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("watch: %v", err)
	}
	if _, err := c.Ask(ctx, connect.NewRequest(&oapv1.AskRequest{})); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("ask: %v", err)
	}
}
