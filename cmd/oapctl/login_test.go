package main

import (
	"context"
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
	pm := http.NewServeMux()
	pm.Handle((&agentapi.Server{Service: svc}).Handler())
	pm.Handle((&ownerapi.Server{Service: svc, Auth: agentapi.Bearer(svc, nil)}).Handler())
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
