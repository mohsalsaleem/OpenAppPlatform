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

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/mcp"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/ownerapi"
)

// twoListeners is oapd's pair of listeners on a simulated host: the local
// socket (no token means the owner) and the public one (tokens only).
func twoListeners(t *testing.T) (local, public string) {
	t.Helper()
	clock := sim.NewClock(time.Now().UTC())
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{"agents": {"owner": {"scope": {"applications": ["*"]}, "may": ["scale", "spec-patch"]}}}`))
	desired.SetEnvelope(context.Background(), env, "owner", "test")
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	lm := http.NewServeMux()
	lm.Handle((&agentapi.Server{Service: svc, Caller: &service.Owner}).Handler())
	lm.Handle((&ownerapi.Server{Service: svc, Auth: agentapi.Bearer(svc, &service.Owner)}).Handler())
	lm.Handle(agentapi.BriefingPath, agentapi.BriefingHandler(svc, agentapi.Bearer(svc, &service.Owner)))
	pm := http.NewServeMux()
	pm.Handle((&agentapi.Server{Service: svc}).Handler())
	pm.Handle((&ownerapi.Server{Service: svc, Auth: agentapi.Bearer(svc, nil)}).Handler())
	pm.Handle(agentapi.BriefingPath, agentapi.BriefingHandler(svc, agentapi.Bearer(svc, nil)))
	pm.Handle("/mcp", &mcp.Server{Service: svc, Auth: agentapi.Bearer(svc, nil)})
	l, p := httptest.NewServer(lm), httptest.NewServer(pm)
	t.Cleanup(l.Close)
	t.Cleanup(p.Close)
	return l.URL, p.URL
}

// cli runs oapctl with the given environment, in a config dir shared by
// the whole test (the agent's key and token live there).
func cli(t *testing.T, dir string, env map[string]string, args ...string) (int, string) {
	t.Helper()
	for _, k := range []string{"OAP_AGENT", "OAP_AGENT_URL"} {
		t.Setenv(k, env[k])
	}
	h := newHarness(t, false, "", "")
	h.a.configDir = dir
	h.a.now = time.Now
	code := h.run(args...)
	return code, h.out.String()
}

func TestAgentLoginEndToEnd(t *testing.T) {
	local, public := twoListeners(t)
	dir := t.TempDir()
	owner := map[string]string{"OAP_AGENT_URL": local}
	agent := map[string]string{"OAP_AGENT": "claude"}
	file := filepath.Join(t.TempDir(), "oap.yaml")
	os.WriteFile(file, []byte(parityYAML), 0o600)
	if code, out := cli(t, dir, owner, "app", "apply", file, "--yes"); code != 0 {
		t.Fatalf("owner apply: %s", out)
	}
	// Without a token the public listener refuses even observe.
	if code, out := cli(t, dir, map[string]string{"OAP_AGENT_URL": public}, "observe"); code == 0 || !strings.Contains(out, "unauthenticated") {
		t.Fatalf("public without a token: %d %s", code, out)
	}

	done := make(chan string, 1)
	go func() {
		h := newHarness(t, false, "", "")
		h.a.configDir, h.a.now = dir, time.Now
		h.run("login", public, "--agent", "claude")
		done <- h.out.String()
	}()
	var codeLine string
	for i := 0; i < 50 && codeLine == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		_, out := cli(t, dir, owner, "owner", "logins")
		if strings.Contains(out, "claude") {
			codeLine = strings.Fields(out)[0]
		}
	}
	if codeLine == "" {
		t.Fatal("the login never appeared to the owner")
	}
	if code, out := cli(t, dir, owner, "owner", "approve", codeLine, "--app", "site", "--may", "scale"); code != 0 || !strings.Contains(out, "Trusted identity: true") {
		t.Fatalf("approve: %d %s", code, out)
	}
	if out := <-done; !strings.Contains(out, "Signed in to "+public+" as claude") {
		t.Fatalf("login: %s", out)
	}
	if st, _ := os.Stat(filepath.Join(dir, "agents/claude/credentials.json")); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatal("credentials not saved 0600")
	}

	if code, out := cli(t, dir, agent, "observe", "site"); code != 0 || !strings.Contains(out, "site") {
		t.Fatalf("agent observe: %s", out)
	}
	if code, out := cli(t, dir, agent, "observe", "blog"); code == 0 || !strings.Contains(out, "may not act on blog") {
		t.Fatalf("out of scope: %s", out)
	}
	if code, out := cli(t, dir, agent, "app", "scale", "site", "web", "2", "--yes"); code != 0 {
		t.Fatalf("agent scale: %s", out)
	}
	if code, out := cli(t, dir, agent, "owner", "agents"); code == 0 || !strings.Contains(out, "only the owner") {
		t.Fatalf("agent used the owner API: %s", out)
	}

	cli(t, dir, owner, "owner", "pause", "claude")
	if code, out := cli(t, dir, agent, "observe", "site"); code == 0 || !strings.Contains(out, "paused") {
		t.Fatalf("paused agent: %s", out)
	}
	cli(t, dir, owner, "owner", "resume", "claude")
	if code, out := cli(t, dir, agent, "observe", "site"); code != 0 {
		t.Fatalf("resumed agent: %s", out)
	}

	cli(t, dir, owner, "owner", "revoke", "claude")
	if code, out := cli(t, dir, agent, "observe", "site"); code == 0 || !strings.Contains(out, "revoked") {
		t.Fatalf("revoked token: %s", out)
	}
	// Trusted identity: signing in again needs no owner.
	if code, out := cli(t, dir, nil, "login", public, "--agent", "claude"); code != 0 || strings.Contains(out, "Waiting for the owner") {
		t.Fatalf("trusted re-login: %d %s", code, out)
	}
	if code, out := cli(t, dir, agent, "observe", "site"); code != 0 {
		t.Fatalf("after re-login: %s", out)
	}
	// Forgetting the identity brings the owner back.
	cli(t, dir, owner, "owner", "revoke", "claude", "--forget")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h := newHarness(t, false, "", "")
	h.a.configDir, h.a.now = dir, time.Now
	t.Setenv("OAP_AGENT", "")
	h.a.run(ctx, []string{"login", public, "--agent", "claude"})
	if !strings.Contains(h.out.String(), "Waiting for the owner") {
		t.Fatalf("forgotten identity: %s", h.out.String())
	}
}

// TestAskEndToEnd: an agent asks and waits; the owner lists and answers;
// the agent prints the answer.
func TestAskEndToEnd(t *testing.T) {
	local, public := twoListeners(t)
	dir := t.TempDir()
	owner := map[string]string{"OAP_AGENT_URL": local}
	go func() {
		h := newHarness(t, false, "", "")
		h.a.configDir, h.a.now = dir, time.Now
		h.run("login", public, "--agent", "claude")
	}()
	var code string
	for i := 0; i < 50 && code == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		if _, out := cli(t, dir, owner, "owner", "logins"); strings.Contains(out, "claude") {
			code = strings.Fields(out)[0]
		}
	}
	cli(t, dir, owner, "owner", "approve", code, "--app", "*")
	for i := 0; i < 50; i++ { // the login finishes in the background
		if _, err := os.Stat(filepath.Join(dir, "agents/claude/credentials.json")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	done := make(chan string, 1)
	go func() {
		_, out := cli(t, dir, map[string]string{"OAP_AGENT": "claude"}, "ask", "Which region?", "--kind", "decision", "--option", "eu", "--option", "us", "--wait")
		done <- out
	}()
	var askID string
	for i := 0; i < 50 && askID == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		if _, out := cli(t, dir, owner, "owner", "asks"); strings.Contains(out, "Which region?") {
			askID = strings.Fields(out)[0]
			if !strings.Contains(out, "[eu | us]") {
				t.Errorf("asks: %s", out)
			}
		}
	}
	if askID == "" {
		t.Fatal("the ask never reached the owner")
	}
	if c, out := cli(t, dir, owner, "owner", "answer", askID, "eu"); c != 0 {
		t.Fatalf("answer: %s", out)
	}
	select {
	case out := <-done:
		if !strings.Contains(out, "The owner answered: eu") {
			t.Fatalf("agent: %s", out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent never got the answer")
	}
}

// TestBriefingEndToEnd: the owner sets house rules; a signed-in agent
// reads its briefing through the CLI, HTTP and MCP; without a token the
// public briefing is refused.
func TestBriefingEndToEnd(t *testing.T) {
	local, public := twoListeners(t)
	dir := t.TempDir()
	owner := map[string]string{"OAP_AGENT_URL": local}
	file := filepath.Join(t.TempDir(), "oap.yaml")
	os.WriteFile(file, []byte(parityYAML), 0o600)
	cli(t, dir, owner, "app", "apply", file, "--yes")
	rules := filepath.Join(t.TempDir(), "briefing.md")
	os.WriteFile(rules, []byte("Scale up only after 18:00 UTC.\n"), 0o600)
	if code, out := cli(t, dir, owner, "owner", "briefing", "site", "--file", rules); code != 0 || !strings.Contains(out, "House rules for site saved") {
		t.Fatalf("set briefing: %s", out)
	}
	go func() {
		h := newHarness(t, false, "", "")
		h.a.configDir, h.a.now = dir, time.Now
		h.run("login", public, "--agent", "claude")
	}()
	var code string
	for i := 0; i < 50 && code == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		if _, out := cli(t, dir, owner, "owner", "logins"); strings.Contains(out, "claude") {
			code = strings.Fields(out)[0]
		}
	}
	cli(t, dir, owner, "owner", "approve", code, "--app", "site")
	var cr creds
	for i := 0; i < 50 && cr.Token == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		if b, err := os.ReadFile(filepath.Join(dir, "agents/claude/credentials.json")); err == nil {
			json.Unmarshal(b, &cr)
		}
	}
	if c, out := cli(t, dir, map[string]string{"OAP_AGENT": "claude"}, "briefing", "site"); c != 0 ||
		!strings.Contains(out, "> Scale up only after 18:00 UTC.") || !strings.Contains(out, "You are agent `claude`") {
		t.Fatalf("CLI briefing: %s", out)
	}
	get := func(token string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, public+agentapi.BriefingPath+"?app=site", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if status, _ := get(""); status != http.StatusUnauthorized {
		t.Fatalf("briefing without a token: %d", status)
	}
	if status, body := get(cr.Token); status != 200 || !strings.Contains(body, "Scale up only after 18:00 UTC.") {
		t.Fatalf("briefing with a token: %d %s", status, body)
	}
	rpc := func(method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, _ := http.NewRequest(http.MethodPost, public+"/mcp", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+cr.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	list := fmt.Sprint(rpc("resources/list", map[string]any{}))
	if !strings.Contains(list, "oap://briefing/site") {
		t.Fatalf("MCP resources: %s", list)
	}
	read := fmt.Sprint(rpc("resources/read", map[string]any{"uri": "oap://briefing/site"}))
	if !strings.Contains(read, "Scale up only after 18:00 UTC.") || !strings.Contains(read, "text/markdown") {
		t.Fatalf("MCP read: %s", read)
	}
}
