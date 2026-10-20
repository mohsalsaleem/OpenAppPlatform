package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/gitrepo"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/notify"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/secretstore"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sqlite"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/listeners"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/wire"
)

// R3.9: the whole surface, attacked by an agent with a narrow scope, on a
// fresh install with the real journal, desired-state repository and secret
// store. Every response from both listeners, every CLI output and oapd's
// log are recorded, then scanned for the secret value and for tokens.

const shopYAML = `api_version: oap.dev/v1
kind: Application
name: shop
environment: production
components:
  web:
    kind: web
    image: reg/shop:1
    port: 8080
    secrets:
      DB_PASSWORD: shop-db
ingress:
  domain: shop.example.com
  routes:
    /:
      component: web
`

const blogYAML = `api_version: oap.dev/v1
kind: Application
name: blog
environment: production
components:
  web:
    kind: web
    image: reg/blog:1
    port: 8080
ingress:
  domain: blog.example.com
  routes:
    /:
      component: web
`

// lockedBuffer collects writes from many goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// tee records a response body as it is written.
type tee struct {
	http.ResponseWriter
	to io.Writer
}

func (w tee) Write(b []byte) (int, error) { w.to.Write(b); return w.ResponseWriter.Write(b) }
func (w tee) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w tee) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type securityRig struct {
	t             *testing.T
	svc           *service.Service
	clock         *sim.Clock
	rt            *sim.Runtime
	data, dir     string // oapd's data directory; oapctl's config directory
	local, public string
	h2            *http.Client
	seen, logs    lockedBuffer // every response body; oapd's log
	cliOut        lockedBuffer // every CLI output
	secret        string
}

func newSecurityRig(t *testing.T) *securityRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &securityRig{t: t, data: t.TempDir(), dir: t.TempDir()}
	r.clock = sim.NewClock(time.Now().UTC())
	journal, err := sqlite.Open(ctx, filepath.Join(r.data, "oap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { journal.Close() })
	t.Cleanup(cancel)
	desired, err := gitrepo.Open(ctx, filepath.Join(r.data, "desired"))
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(r.data, "secret.key")
	if err := secretstore.CreateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	secrets, err := secretstore.Open(filepath.Join(r.data, "secrets.json"), keyPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 18)
	rand.Read(raw)
	r.secret = "pw-" + hex.EncodeToString(raw)
	if err := secrets.Set("shop-db", r.secret); err != nil {
		t.Fatal(err)
	}
	r.rt = sim.NewRuntime(r.clock, model.HostObservation{Name: "sim", MemoryTotalMiB: 4096})
	r.rt.Secrets = secrets
	edge := sim.NewEdge()
	log := slog.New(slog.NewTextHandler(&r.logs, nil))
	r.svc = &service.Service{Desired: desired, Journal: journal, Clock: r.clock, Notifier: notify.Log{Logger: log},
		Observer: &engine.Observer{Runtime: r.rt, Clock: r.clock},
		Executor: &engine.Executor{Runtime: r.rt, Edge: edge, Journal: journal, Desired: desired, Clock: r.clock}}
	if err := r.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	r.local, r.public = r.serve(listeners.Socket(r.svc)), r.serve(listeners.Public(r.svc))
	var cp http.Protocols
	cp.SetUnencryptedHTTP2(true)
	r.h2 = &http.Client{Transport: &http.Transport{Protocols: &cp}}
	return r
}

// serve starts a listener like oapd's (HTTP/1.1 and cleartext HTTP/2) that
// records every response body.
func (r *securityRig) serve(h http.Handler) string {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h.ServeHTTP(tee{w, &r.seen}, req)
	}))
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	ts.Config.Protocols = &p
	ts.Start()
	r.t.Cleanup(ts.Close)
	return ts.URL
}

// cli runs oapctl and records its output.
func (r *securityRig) cli(env map[string]string, args ...string) (int, string) {
	r.t.Helper()
	code, out := cli(r.t, r.dir, env, args...)
	r.cliOut.Write([]byte(out))
	return code, out
}

// login signs an agent in through the CLI on the public listener and has
// the owner approve it with a scope. It returns the agent's token.
func (r *securityRig) login(agent, apps, may string) string {
	r.t.Helper()
	owner := map[string]string{"OAP_AGENT_URL": r.local}
	done := make(chan string, 1)
	go func() {
		h := newHarness(r.t, false, "", "")
		h.a.configDir, h.a.now = r.dir, time.Now
		h.run("login", r.public, "--agent", agent)
		done <- h.out.String()
	}()
	code := ""
	for i := 0; i < 100 && code == ""; i++ {
		time.Sleep(20 * time.Millisecond)
		res, err := r.svc.ListLogins(context.Background(), service.Owner)
		if err != nil {
			r.t.Fatal(err)
		}
		for _, l := range res.Logins {
			if l.Agent == agent {
				code = l.UserCode
			}
		}
	}
	if code == "" {
		r.t.Fatalf("%s's login never reached the owner", agent)
	}
	if c, out := r.cli(owner, "owner", "approve", code, "--app", apps, "--may", may); c != 0 {
		r.t.Fatalf("approve %s: %s", agent, out)
	}
	r.cliOut.Write([]byte(<-done))
	b, err := os.ReadFile(filepath.Join(r.dir, "agents", agent, "credentials.json"))
	if err != nil {
		r.t.Fatal(err)
	}
	var cr creds
	if err := json.Unmarshal(b, &cr); err != nil || cr.Token == "" {
		r.t.Fatalf("credentials of %s: %v", agent, err)
	}
	return cr.Token
}

// withToken adds a token to every request, as an agent's client does.
type withToken struct {
	token string
	next  http.RoundTripper
}

func (b withToken) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.next.RoundTrip(req)
}

func (r *securityRig) client(token string) *http.Client {
	return &http.Client{Transport: withToken{token, r.h2.Transport}}
}

// protocols are the agent API's wire protocols: Connect (binary and JSON),
// gRPC and gRPC-Web.
func (r *securityRig) protocols(base, token string) map[string]oapv1connect.AgentServiceClient {
	hc := r.client(token)
	return map[string]oapv1connect.AgentServiceClient{
		"connect":      oapv1connect.NewAgentServiceClient(hc, base),
		"connect-json": oapv1connect.NewAgentServiceClient(hc, base, connect.WithProtoJSON()),
		"grpc":         oapv1connect.NewAgentServiceClient(hc, base, connect.WithGRPC()),
		"grpc-web":     oapv1connect.NewAgentServiceClient(hc, base, connect.WithGRPCWeb()),
	}
}

// mcp posts one JSON-RPC call to /mcp and returns the status and body.
func (r *securityRig) mcp(base, token, method string, params any) (int, map[string]any) {
	r.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, base+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := r.client(token).Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// mcpRefused reports whether an MCP response is an error: a JSON-RPC error
// or a tool result marked isError.
func mcpRefused(out map[string]any) bool {
	if out["error"] != nil {
		return true
	}
	res, _ := out["result"].(map[string]any)
	isErr, _ := res["isError"].(bool)
	return isErr
}

func (r *securityRig) get(base, token, path string) (int, string) {
	r.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	res, err := r.client(token).Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func codeOf(err error) connect.Code {
	if err == nil {
		return 0
	}
	return connect.CodeOf(err)
}

// fixture is what the owner set up before the agents arrived.
type fixture struct {
	alice, bob   string // tokens: alice may act on shop, bob on blog
	blogProposal string // the owner's proposal on blog, not applied
	blogOp       string // an operation on blog
	shopProposal string // the owner's proposal on shop, allowed but the owner's
	bobAsk       string // bob's question to the owner
}

func (r *securityRig) setUp() fixture {
	r.t.Helper()
	ctx := context.Background()
	owner := map[string]string{"OAP_AGENT_URL": r.local}
	for name, y := range map[string]string{"shop": shopYAML, "blog": blogYAML} {
		file := filepath.Join(r.t.TempDir(), name+".yaml")
		os.WriteFile(file, []byte(y), 0o600)
		if code, out := r.cli(owner, "app", "apply", file, "--yes"); code != 0 {
			r.t.Fatalf("owner apply %s: %s", name, out)
		}
	}
	// The secret really reached the shop's instance: the scan isn't vacuous.
	got := ""
	for _, ref := range r.rt.Running() {
		if ref.Application == "shop" {
			got = r.rt.Env(ref)["DB_PASSWORD"]
		}
	}
	if got != r.secret {
		r.t.Fatal("the shop's instance didn't get its secret; the scan would prove nothing")
	}

	var f fixture
	f.alice = r.login("alice", "shop", "scale,deploy,rollback,configure,spec-patch")
	f.bob = r.login("bob", "blog", "scale")
	scale := func(app string, n int32) *oapv1.Intent {
		return &oapv1.Intent{Application: app, Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "web", Instances: n}}}
	}
	p, err := r.svc.Propose(ctx, service.Owner, &oapv1.ProposeRequest{Intent: scale("blog", 2)})
	if err != nil {
		r.t.Fatal(err)
	}
	a, err := r.svc.Apply(ctx, service.Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
	if err != nil {
		r.t.Fatal(err)
	}
	f.blogOp = a.OperationId
	r.svc.Await(ctx, service.Owner, &oapv1.AwaitRequest{OperationId: f.blogOp}, func(*oapv1.Event) error { return nil })
	if p, err = r.svc.Propose(ctx, service.Owner, &oapv1.ProposeRequest{Intent: scale("blog", 3)}); err != nil {
		r.t.Fatal(err)
	}
	f.blogProposal = p.Proposal.Id
	if p, err = r.svc.Propose(ctx, service.Owner, &oapv1.ProposeRequest{Intent: scale("shop", 3)}); err != nil {
		r.t.Fatal(err)
	}
	f.shopProposal = p.Proposal.Id
	bob, err := r.svc.Authenticate(ctx, f.bob)
	if err != nil {
		r.t.Fatal(err)
	}
	ask, err := r.svc.Ask(ctx, bob, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "Is the blog's private-launch-date still Friday?"})
	if err != nil {
		r.t.Fatal(err)
	}
	f.bobAsk = ask.AskId
	r.svc.Observer.Resync(ctx)
	return f
}

// TestAgentStaysInScopeOnEverySurface: an agent scoped to one application
// can't read or act on another, or use the owner's API, through Connect,
// gRPC, gRPC-Web, JSON, MCP over HTTP and stdio, the briefing endpoint or
// the CLI, on either listener; tokens that are missing, unknown, revoked,
// paused or expired are refused everywhere; and no response, CLI output,
// log, journal or desired-state commit holds the secret value or a token.
func TestAgentStaysInScopeOnEverySurface(t *testing.T) {
	r := newSecurityRig(t)
	f := r.setUp()
	ctx := context.Background()
	outOfScope := []string{"blog", "private-launch-date"} // what alice must never see

	noLeak := func(where, s string) {
		t.Helper()
		for _, w := range outOfScope {
			if strings.Contains(s, w) {
				t.Errorf("%s shows alice %q: %s", where, w, s)
			}
		}
	}

	// Agent API: every protocol, both listeners (on the socket a token
	// makes the request the agent's, not the owner's).
	for _, base := range []string{r.public, r.local} {
		listener := map[string]string{r.public: "public", r.local: "socket"}[base]
		for proto, c := range r.protocols(base, f.alice) {
			at := listener + "/" + proto
			denied := func(what string, err error) {
				t.Helper()
				if codeOf(err) != connect.CodePermissionDenied {
					t.Errorf("%s: %s: want permission denied, got %v", at, what, err)
				}
			}
			_, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: "blog"}))
			denied("observe blog", err)
			_, err = c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: "blog", Briefing: true}))
			denied("observe blog's briefing", err)
			_, err = c.Explain(ctx, connect.NewRequest(&oapv1.ExplainRequest{Application: "blog"}))
			denied("explain blog", err)
			_, err = c.Propose(ctx, connect.NewRequest(&oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: "blog",
				Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "web", Instances: 4}}}}))
			denied("propose on blog", err)
			_, err = c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: f.blogProposal}))
			denied("apply the owner's blog proposal", err)
			_, err = c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: f.shopProposal}))
			denied("apply the owner's shop proposal", err)
			_, err = c.Ask(ctx, connect.NewRequest(&oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_APPROVAL, ProposalId: f.blogProposal, Question: "?"}))
			denied("ask approval for blog", err)
			aw, err := c.Await(ctx, connect.NewRequest(&oapv1.AwaitRequest{OperationId: f.blogOp}))
			if err == nil {
				for aw.Receive() {
					t.Errorf("%s: await blog's operation sent %v", at, aw.Msg())
				}
				err = aw.Err()
			}
			denied("await blog's operation", err)
			w, err := c.Watch(ctx, connect.NewRequest(&oapv1.WatchRequest{Application: "blog", SinceRevision: 1}))
			if err == nil {
				for w.Receive() {
					t.Errorf("%s: watch blog sent %v", at, w.Msg())
				}
				err = w.Err()
			}
			denied("watch blog", err)

			// What alice may see holds nothing of blog or bob.
			world, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Detail: true, Briefing: true}))
			if err != nil {
				t.Fatalf("%s: observe: %v", at, err)
			}
			b, _ := wire.Marshal(world.Msg)
			noLeak(at+" observe", string(b))
			wctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			w, err = c.Watch(wctx, connect.NewRequest(&oapv1.WatchRequest{SinceRevision: 1}))
			n := 0
			if err == nil {
				for w.Receive() {
					n++
					b, _ := wire.Marshal(w.Msg())
					noLeak(at+" watch", string(b))
				}
			}
			cancel()
			if n == 0 {
				t.Errorf("%s: watch sent alice nothing, not even shop's events", at)
			}
		}
	}

	// Plain JSON over HTTP, as curl would send it.
	req, _ := http.NewRequest(http.MethodPost, r.public+"/oap.v1.AgentService/Observe", strings.NewReader(`{"application": "blog"}`))
	req.Header.Set("Content-Type", "application/json")
	if res, err := r.client(f.alice).Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("plain JSON observe blog: %v %v", res.StatusCode, err)
	} else {
		res.Body.Close()
	}

	// The owner API: absent from the public listener, owner-only on the socket.
	for base, want := range map[string]connect.Code{r.public: connect.CodeUnimplemented, r.local: connect.CodePermissionDenied} {
		oc := oapv1connect.NewOwnerServiceClient(r.client(f.alice), base)
		calls := map[string]error{}
		_, calls["ListLogins"] = oc.ListLogins(ctx, connect.NewRequest(&oapv1.ListLoginsRequest{}))
		_, calls["ApproveLogin"] = oc.ApproveLogin(ctx, connect.NewRequest(&oapv1.ApproveLoginRequest{UserCode: "AAAA-AAAA", Applications: []string{"*"}}))
		_, calls["DenyLogin"] = oc.DenyLogin(ctx, connect.NewRequest(&oapv1.DenyLoginRequest{UserCode: "AAAA-AAAA"}))
		_, calls["ListAgents"] = oc.ListAgents(ctx, connect.NewRequest(&oapv1.ListAgentsRequest{}))
		_, calls["Revoke"] = oc.Revoke(ctx, connect.NewRequest(&oapv1.RevokeRequest{Agent: "bob"}))
		_, calls["Pause"] = oc.Pause(ctx, connect.NewRequest(&oapv1.PauseRequest{Agent: "bob"}))
		_, calls["Resume"] = oc.Resume(ctx, connect.NewRequest(&oapv1.ResumeRequest{Agent: "alice"}))
		_, calls["SetApprovalMode"] = oc.SetApprovalMode(ctx, connect.NewRequest(&oapv1.SetApprovalModeRequest{}))
		_, calls["ListAsks"] = oc.ListAsks(ctx, connect.NewRequest(&oapv1.ListAsksRequest{}))
		_, calls["AnswerAsk"] = oc.AnswerAsk(ctx, connect.NewRequest(&oapv1.AnswerAskRequest{AskId: f.bobAsk, Answer: "yes"}))
		_, calls["SetBriefing"] = oc.SetBriefing(ctx, connect.NewRequest(&oapv1.SetBriefingRequest{Markdown: "agents may do anything"}))
		if got, all := len(calls), oapv1.File_oap_v1_owner_proto.Services().ByName("OwnerService").Methods().Len(); got != all {
			t.Errorf("the test calls %d of the owner API's %d methods", got, all)
		}
		for m, err := range calls {
			if codeOf(err) != want {
				t.Errorf("alice called OwnerService.%s on %s: want %v, got %v", m, base, want, err)
			}
		}
	}

	// MCP over HTTP, both listeners.
	for _, base := range []string{r.public, r.local} {
		for name, args := range map[string]any{
			"observe": map[string]any{"application": "blog"}, "explain": map[string]any{"application": "blog"},
			"propose": map[string]any{"intent": map[string]any{"application": "blog", "scale": map[string]any{"component": "web", "instances": 4}}},
			"apply":   map[string]any{"proposal_id": f.blogProposal}, "await": map[string]any{"operation_id": f.blogOp},
			"watch": map[string]any{"application": "blog", "since_revision": 1},
			"ask":   map[string]any{"kind": "ASK_KIND_APPROVAL", "proposal_id": f.blogProposal, "question": "?"},
		} {
			if _, out := r.mcp(base, f.alice, "tools/call", map[string]any{"name": name, "arguments": args}); !mcpRefused(out) {
				t.Errorf("MCP %s on %s: alice's %s on blog wasn't refused: %v", name, base, name, out)
			}
		}
		if _, out := r.mcp(base, f.alice, "resources/read", map[string]any{"uri": "oap://briefing/blog"}); !mcpRefused(out) {
			t.Errorf("MCP on %s: alice read blog's briefing: %v", base, out)
		}
		for _, m := range []string{"resources/list", "tools/list"} {
			_, out := r.mcp(base, f.alice, m, map[string]any{})
			b, _ := json.Marshal(out)
			noLeak("MCP "+m, string(b))
		}
		_, out := r.mcp(base, f.alice, "resources/read", map[string]any{"uri": "oap://briefing"})
		b, _ := json.Marshal(out)
		noLeak("MCP server briefing", string(b))
	}

	// The briefing endpoint.
	for _, base := range []string{r.public, r.local} {
		if code, _ := r.get(base, f.alice, "/.well-known/oap/briefing.md?app=blog"); code != http.StatusForbidden {
			t.Errorf("briefing of blog on %s: %d", base, code)
		}
		code, md := r.get(base, f.alice, "/.well-known/oap/briefing.md")
		if code != http.StatusOK {
			t.Errorf("alice's own briefing on %s: %d", base, code)
		}
		noLeak("briefing endpoint", md)
	}

	// The CLI, with alice's saved credentials, and its MCP bridge.
	agent := map[string]string{"OAP_AGENT": "alice"}
	for _, args := range [][]string{{"observe", "blog"}, {"explain", "blog"}, {"watch", "blog", "--since", "1", "--limit", "1"},
		{"briefing", "blog"}, {"app", "scale", "blog", "web", "4", "--yes"}, {"owner", "agents"}, {"owner", "asks"}} {
		if code, out := r.cli(agent, args...); code == 0 {
			t.Errorf("oapctl %s as alice succeeded: %s", strings.Join(args, " "), out)
		}
	}
	if code, out := r.cli(agent, "briefing"); code != 0 {
		t.Errorf("alice's own briefing: %s", out)
	} else {
		noLeak("oapctl briefing", out)
	}
	stdio := func(env map[string]string, line string) string {
		t.Helper()
		for _, k := range []string{"OAP_AGENT", "OAP_AGENT_URL"} {
			t.Setenv(k, env[k])
		}
		h := newHarness(t, false, line+"\n", "")
		h.a.configDir, h.a.now = r.dir, time.Now
		h.run("mcp")
		r.cliOut.Write(h.out.Bytes())
		return h.out.String()
	}
	call := `{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "observe", "arguments": {"application": "blog"}}}`
	// Even pointed at the owner's socket, oapctl mcp acts as the agent
	// OAP_AGENT names.
	for _, env := range []map[string]string{agent, {"OAP_AGENT": "alice", "OAP_AGENT_URL": r.local}} {
		var out map[string]any
		if err := json.Unmarshal([]byte(stdio(env, call)), &out); err != nil || !mcpRefused(out) {
			t.Errorf("oapctl mcp as alice (%v) observed blog: %v %v", env, out, err)
		}
	}

	// Missing and unknown tokens on the public listener; an unknown token on
	// the socket isn't taken for the owner.
	for _, tok := range []string{"", "oapt_" + strings.Repeat("x", 32)} {
		for _, base := range []string{r.public, r.local} {
			if tok == "" && base == r.local {
				continue // no token on the socket is the owner, reached over SSH
			}
			for proto, c := range r.protocols(base, tok) {
				_, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{}))
				_, err2 := c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: f.shopProposal}))
				_, err3 := c.Ask(ctx, connect.NewRequest(&oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "?"}))
				for _, e := range []error{err, err2, err3} {
					if codeOf(e) != connect.CodeUnauthenticated {
						t.Errorf("%s %s token %q: want unauthenticated, got %v", base, proto, tok, e)
					}
				}
			}
			if code, _ := r.mcp(base, tok, "tools/call", map[string]any{"name": "observe", "arguments": map[string]any{}}); code != http.StatusUnauthorized {
				t.Errorf("MCP on %s with token %q: %d", base, tok, code)
			}
			if code, _ := r.get(base, tok, "/.well-known/oap/briefing.md"); code != http.StatusUnauthorized {
				t.Errorf("briefing on %s with token %q: %d", base, tok, code)
			}
		}
	}

	// Paused, then revoked: refused on every surface from the next request.
	refusedEverywhere := func(state string, token string) {
		t.Helper()
		for _, base := range []string{r.public, r.local} {
			for proto, c := range r.protocols(base, token) {
				if _, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: "shop"})); err == nil {
					t.Errorf("%s alice on %s/%s observed", state, base, proto)
				}
			}
			if code, _ := r.mcp(base, token, "tools/call", map[string]any{"name": "observe", "arguments": map[string]any{}}); code != http.StatusUnauthorized {
				t.Errorf("%s alice used MCP on %s: %d", state, base, code)
			}
			if code, _ := r.get(base, token, "/.well-known/oap/briefing.md"); code == http.StatusOK {
				t.Errorf("%s alice read the briefing on %s", state, base)
			}
		}
		if code, out := r.cli(agent, "observe", "shop"); code == 0 {
			t.Errorf("%s alice used the CLI: %s", state, out)
		}
	}
	if _, err := r.svc.Pause(ctx, service.Owner, &oapv1.PauseRequest{Agent: "alice"}); err != nil {
		t.Fatal(err)
	}
	refusedEverywhere("paused", f.alice)
	r.svc.Resume(ctx, service.Owner, &oapv1.ResumeRequest{Agent: "alice"})
	if _, err := r.svc.Revoke(ctx, service.Owner, &oapv1.RevokeRequest{Agent: "alice", ForgetIdentity: true}); err != nil {
		t.Fatal(err)
	}
	refusedEverywhere("revoked", f.alice)

	// Expired: bob's one-hour token, an hour and a minute later.
	r.clock.Advance(61 * time.Minute)
	for _, base := range []string{r.public, r.local} {
		for proto, c := range r.protocols(base, f.bob) {
			if _, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{})); codeOf(err) != connect.CodeUnauthenticated {
				t.Errorf("expired token on %s/%s: %v", base, proto, err)
			}
		}
	}

	r.scan(f.alice, f.bob)
}

// scan looks for the secret value, in plain and encoded forms, in every
// response, CLI output and log line, the journal and the desired-state
// repository with its history; and for tokens outside the responses that
// handed them to their agent.
func (r *securityRig) scan(tokens ...string) {
	t := r.t
	t.Helper()
	gitLog, err := exec.Command("git", "-C", filepath.Join(r.data, "desired"), "log", "--all", "-p", "--format=%H %an %s%n%b").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v %s", err, gitLog)
	}
	var files strings.Builder
	filepath.Walk(r.data, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			b, _ := os.ReadFile(p)
			files.Write(b)
		}
		return nil
	})
	places := map[string]string{
		"responses":             r.seen.String(),
		"CLI output":            r.cliOut.String(),
		"oapd's log":            r.logs.String(),
		"desired-state history": string(gitLog),
		"oapd's data files":     files.String(),
	}
	if !strings.Contains(places["responses"], "shop") || len(places["oapd's log"]) == 0 || len(places["desired-state history"]) == 0 {
		t.Fatal("the recordings are empty; the scan would prove nothing")
	}
	forms := func(s string) []string {
		return []string{s, base64.StdEncoding.EncodeToString([]byte(s)), base64.RawURLEncoding.EncodeToString([]byte(s)), hex.EncodeToString([]byte(s))}
	}
	for where, text := range places {
		for _, form := range forms(r.secret) {
			if strings.Contains(text, form) {
				t.Errorf("the secret value appears in %s", where)
			}
		}
	}
	for _, tok := range tokens {
		for where, text := range places {
			if where == "responses" || where == "CLI output" {
				// The login response hands the token to its agent, and the
				// CLI saves it in the agent's credentials; nothing else may.
				text = strings.Replace(text, tok, "", 1)
			}
			if where == "oapd's data files" {
				continue // checked below: the journal keeps only a hash
			}
			if strings.Contains(text, tok) {
				t.Errorf("a token appears in %s", where)
			}
		}
		db, _ := os.ReadFile(filepath.Join(r.data, "oap.db"))
		wal, _ := os.ReadFile(filepath.Join(r.data, "oap.db-wal"))
		if bytes.Contains(append(db, wal...), []byte(tok)) {
			t.Error("a token appears in the journal")
		}
	}
}

// TestFreshInstallAutonomy: the owner's decision for R3.9. On a fresh
// install (no envelope committed), an approved agent's routine changes
// run without the owner, and sensitive ones wait for the owner's approval.
func TestFreshInstallAutonomy(t *testing.T) {
	r := newSecurityRig(t)
	f := r.setUp()
	ctx := context.Background()
	c := oapv1connect.NewAgentServiceClient(r.client(f.alice), r.public)
	if _, sha, _ := r.svc.Desired.Envelope(ctx); sha == "" {
		t.Fatal("no envelope")
	}

	routine, err := c.Propose(ctx, connect.NewRequest(&oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: "shop",
		Change: &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "web", Instances: 2}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if v := routine.Msg.Proposal.GetVerdict(); v != oapv1.Verdict_VERDICT_ALLOWED {
		t.Fatalf("scaling is routine: want allowed, got %v %v", v, routine.Msg.Proposal.GetReasons())
	}
	if _, err := c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: routine.Msg.Proposal.Id})); err != nil {
		t.Fatalf("routine apply: %v", err)
	}

	patch := map[string]any{"ingress": map[string]any{"domain": "shop2.example.com"}}
	b, _ := json.Marshal(map[string]any{"intent": map[string]any{"application": "shop", "spec_patch": map[string]any{"merge_patch": patch}}})
	req := &oapv1.ProposeRequest{}
	if err := wire.Unmarshal(b, req); err != nil {
		t.Fatal(err)
	}
	sensitive, err := c.Propose(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	if v := sensitive.Msg.Proposal.GetVerdict(); v != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
		t.Fatalf("a domain change is sensitive: want needs approval, got %v %v", v, sensitive.Msg.Proposal.GetReasons())
	}
	_, err = c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: sensitive.Msg.Proposal.Id}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodePermissionDenied {
		t.Fatalf("applying a sensitive change without the owner: %v", err)
	}
}
