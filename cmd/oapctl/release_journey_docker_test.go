package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"google.golang.org/protobuf/types/known/structpb"
)

// The release journey (R5.2): the v0.1 VPS journey on the v0.2 interfaces.
// One application per interface, each run by its own agent: the CLI run is
// the full journey, the Connect and MCP runs are smoke runs. Final checks:
// the apps coexist behind the shared edge, keep serving while oapd is
// stopped, and the host stays out of memory trouble. Gated on
// OAP_TEST_DOCKER=1; OAP_JOURNEY_OUT keeps the receipt.

const fixtureImage = "journey-fixture"

// buildFixture builds the fixture's three image versions on the host.
func buildFixture(t *testing.T, h *journeyHost) {
	t.Helper()
	dir := t.TempDir()
	for _, v := range []string{"v1", "v2", "bad"} {
		build := exec.Command("go", "build", "-ldflags", "-X main.version="+v, "-o", filepath.Join(dir, v, "journeyfixture"), "./testdata/journeyfixture")
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build fixture %s: %v\n%s", v, err, out)
		}
		os.WriteFile(filepath.Join(dir, v, "Dockerfile"), []byte(`FROM busybox:1.37
COPY journeyfixture /journeyfixture
HEALTHCHECK --interval=5s --timeout=3s --retries=3 CMD ["/journeyfixture", "health"]
ENTRYPOINT ["/journeyfixture"]
`), 0o644)
		if out, err := exec.Command("docker", "cp", filepath.Join(dir, v), h.name+":/root/fixture-"+v).CombinedOutput(); err != nil {
			t.Fatalf("copy fixture: %v %s", err, out)
		}
		if out, err := exec.Command("docker", "exec", h.name, "docker", "build", "-q", "-t", fixtureImage+":"+v, "/root/fixture-"+v).CombinedOutput(); err != nil {
			ls, _ := exec.Command("docker", "exec", h.name, "ls", "-la", "/root/fixture-"+v).CombinedOutput()
			local, _ := exec.Command("ls", "-la", filepath.Join(dir, v)).CombinedOutput()
			t.Fatalf("docker build fixture %s: %v\n%s\nhost:\n%s\nlocal:\n%s", v, err, out, ls, local)
		}
	}
}

// journeyApp is the journey's application: web, api (after the migration,
// using the data component), notes (with a volume), worker, kv and the
// migrate job.
func journeyApp(name, domain string, apiInstances int) map[string]any {
	img := fixtureImage + ":v1"
	env := func(role string, more ...string) map[string]any {
		m := map[string]any{"ROLE": role, "APP": name}
		for i := 0; i+1 < len(more); i += 2 {
			m[more[i]] = more[i+1]
		}
		return m
	}
	http := map[string]any{"mode": "http", "path": "/health"}
	return map[string]any{
		"api_version": "oap.dev/v1", "kind": "Application", "name": name, "environment": "production",
		"ingress": map[string]any{"domain": domain, "routes": map[string]any{
			"/": map[string]any{"component": "web"}, "/api": map[string]any{"component": "api"}, "/notes": map[string]any{"component": "notes"}}},
		"components": map[string]any{
			"migrate": map[string]any{"kind": "job", "image": img, "env": env("migrate", "JOB_EXIT_CODE", "0")},
			"kv":      map[string]any{"kind": "data", "image": img, "env": env("kv"), "volumes": map[string]any{"data": "/data"}, "health": map[string]any{"mode": "image"}},
			"api": map[string]any{"kind": "web", "image": img, "port": 80, "instances": apiInstances, "env": env("api"), "health": http,
				"after": []any{"migrate"}, "uses": map[string]any{"kv": map[string]any{"type": "database"}}, "readiness": map[string]any{"timeout_seconds": 30}},
			"notes":  map[string]any{"kind": "web", "image": img, "port": 80, "env": env("notes"), "volumes": map[string]any{"data": "/data"}, "health": http},
			"web":    map[string]any{"kind": "web", "image": img, "port": 80, "env": env("web"), "health": http},
			"worker": map[string]any{"kind": "worker", "image": img, "env": env("worker"), "health": map[string]any{"mode": "image"}},
		},
	}
}

// trafficProbe requests a path on a domain through the edge until stopped.
type trafficProbe struct {
	stop                chan struct{}
	done                chan struct{}
	mu                  sync.Mutex
	requests, failures  int
	versions, instances map[string]int
}

func (h *journeyHost) probe(domain, path string) *trafficProbe {
	p := &trafficProbe{stop: make(chan struct{}), done: make(chan struct{}), versions: map[string]int{}, instances: map[string]int{}}
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		defer close(p.done)
		for {
			select {
			case <-p.stop:
				return
			default:
			}
			req, _ := http.NewRequest(http.MethodGet, h.web+path, nil)
			req.Host = domain
			res, err := client.Do(req)
			p.mu.Lock()
			p.requests++
			if err != nil || res.StatusCode != 200 {
				p.failures++
			} else {
				var v struct{ Version, Instance string }
				b, _ := io.ReadAll(res.Body)
				if json.Unmarshal(b, &v) == nil {
					p.versions[v.Version]++
					p.instances[v.Instance]++
				}
			}
			p.mu.Unlock()
			if res != nil {
				res.Body.Close()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return p
}

func (p *trafficProbe) end() *trafficProbe {
	close(p.stop)
	<-p.done
	return p
}

func (p *trafficProbe) String() string {
	return fmt.Sprintf("%d requests, %d failed, versions %v, %d instances", p.requests, p.failures, p.versions, len(p.instances))
}

// sample takes n requests and returns the probe that saw them.
func (h *journeyHost) sample(domain, path string, n int) *trafficProbe {
	p := h.probe(domain, path)
	for {
		p.mu.Lock()
		r := p.requests
		p.mu.Unlock()
		if r >= n {
			return p.end()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// receipt records the journey as it goes.
type receipt struct {
	mu           sync.Mutex
	Runs         map[string]map[string]any `json:"runs"`
	Final        map[string]any            `json:"final"`
	Observations []string                  `json:"observations"`
}

func (r *receipt) step(run, name string, v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Runs[run] == nil {
		r.Runs[run] = map[string]any{}
	}
	r.Runs[run][name] = v
}

func (r *receipt) observe(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Observations = append(r.Observations, fmt.Sprintf(format, args...))
}

var proposalIDRe = regexp.MustCompile(`"id":\s*"(prop_[A-Za-z0-9]+)"`)
var askIDRe = regexp.MustCompile(`(ask_[A-Za-z0-9_-]+)`)
var opIDRe = regexp.MustCompile(`operation (op_[A-Za-z0-9]+)`)

func TestReleaseJourney(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	h := newJourneyHost(t, "1g")
	buildFixture(t, h)
	rec := &receipt{Runs: map[string]map[string]any{}, Final: map[string]any{}}
	defer func() {
		b, _ := json.MarshalIndent(rec, "", "  ")
		t.Logf("receipt:\n%s", b)
		if out := os.Getenv("OAP_JOURNEY_OUT"); out != "" {
			os.WriteFile(out, b, 0o644)
		}
	}()
	hostSh := func(script string) string {
		t.Helper()
		out, err := exec.Command("docker", "exec", "-i", h.name, "sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	ooms := func() string { return hostSh(`grep '^oom_kill ' /sys/fs/cgroup/memory.events`) }
	oomsBefore := ooms()

	dir := t.TempDir()
	owner := map[string]string{"OAP_AGENT_URL": h.api}
	signIn := func(agent, app string) string {
		t.Helper()
		done := make(chan string, 1)
		go func() {
			hh := newHarness(t, false, "", "")
			hh.a.configDir, hh.a.now = dir, time.Now
			hh.run("login", h.api, "--agent", agent)
			done <- hh.out.String()
		}()
		code := ""
		for i := 0; i < 100 && code == ""; i++ {
			time.Sleep(50 * time.Millisecond)
			if _, out := cli(t, dir, owner, "owner", "logins"); strings.Contains(out, agent) {
				for _, l := range strings.Split(out, "\n") {
					if strings.Contains(l, agent) {
						code = strings.Fields(l)[0]
					}
				}
			}
		}
		if code == "" {
			t.Fatalf("%s's login never reached the owner", agent)
		}
		if c, out := cli(t, dir, owner, "owner", "approve", code, "--app", app, "--may", "scale,deploy,rollback,configure,spec-patch"); c != 0 {
			t.Fatalf("approve %s: %s", agent, out)
		}
		<-done
		b, _ := os.ReadFile(filepath.Join(dir, "agents", agent, "credentials.json"))
		var cr creds
		json.Unmarshal(b, &cr)
		return cr.Token
	}
	answer := func(askID string) {
		t.Helper()
		if c, out := cli(t, dir, owner, "owner", "answer", askID, "approve"); c != 0 {
			t.Fatalf("owner answer %s: %s", askID, out)
		}
	}
	get := func(domain, path string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, h.web+path, nil)
		req.Host = domain
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	// ---- CLI run: the full journey.
	t.Run("cli", func(t *testing.T) {
		const app, domain = "journey-cli", "cli.journey.test"
		signIn("journey-cli", app)
		agent := map[string]string{"OAP_AGENT": "journey-cli"}
		oap := func(args ...string) (int, string) { return cli(t, dir, agent, args...) }
		spec := journeyApp(app, domain, 1)
		file := filepath.Join(t.TempDir(), "oap.yaml")
		write := func() { b, _ := json.Marshal(spec); os.WriteFile(file, b, 0o600) }
		comp := func(name string) map[string]any { return spec["components"].(map[string]any)[name].(map[string]any) }

		// C1: propose; a new domain needs the owner, and nothing starts yet.
		write()
		code, out := oap("app", "apply", file)
		m := proposalIDRe.FindStringSubmatch(out)
		if code != 3 || m == nil || !strings.Contains(out, "needs_approval") {
			t.Fatalf("C1 propose: %d\n%s", code, out)
		}
		prop := m[1]
		if n := hostSh(`docker ps -q --filter label=io.openappplatform.application=` + app + ` | wc -l`); n != "0" {
			t.Fatalf("C1: %s containers started before approval", n)
		}
		rec.step("cli", "proposeNeedsApproval", true)

		// C2: ask the owner, who approves; apply the approved proposal.
		_, out = oap("ask", "--kind", "approval", "--proposal", prop, "Create journey-cli with its domain?")
		ask := askIDRe.FindString(out)
		if ask == "" {
			t.Fatalf("C2 ask: %s", out)
		}
		answer(ask)
		code, out = oap("apply", prop, "--events")
		if code != 0 {
			t.Fatalf("C2 apply: %s", out)
		}
		firstOp := ""
		if r := regexp.MustCompile(`"operation_id":"(op_[A-Za-z0-9]+)"`).FindStringSubmatch(out); r != nil {
			firstOp = r[1]
		}
		job, api := strings.Index(out, "STEP_KIND_RUN_JOB"), strings.Index(out, `"component":"api"`)
		if job < 0 || api < 0 || job > api {
			t.Fatalf("C2: the migration didn't run before the api\n%s", out)
		}
		rec.step("cli", "deployWithMigrationFirst", true)
		if c, b := get(domain, "/api"); c != 200 || !strings.Contains(b, `"version":"v1"`) {
			t.Fatalf("C2 /api: %d %s", c, b)
		}
		if c, b := get(domain, "/notes"); c != 200 || !strings.Contains(b, `"boots":1`) {
			t.Fatalf("C2 /notes: %d %s", c, b)
		}
		rec.step("cli", "serving", true)

		// C4: applying the same proposal again is the same operation.
		code, out = oap("apply", prop)
		if r := opIDRe.FindStringSubmatch(out); code != 0 || r == nil || r[1] != firstOp {
			t.Fatalf("C4 replay: %d %s (first operation %s)", code, out, firstOp)
		}
		if code, out = oap("app", "apply", file, "--yes"); code != 0 || !strings.Contains(out, "already") {
			t.Fatalf("C4 re-apply the file: %d %s", code, out)
		}
		rec.step("cli", "replaySameOperation", true)

		// D1: scale the api to 3 under traffic: no request fails.
		p := h.probe(domain, "/api")
		if code, out = oap("app", "scale", app, "api", "3", "--yes"); code != 0 {
			t.Fatalf("D1 scale: %s", out)
		}
		if p.end(); p.failures != 0 {
			t.Fatalf("D1: %s", p)
		}
		rec.step("cli", "scaleUpFailures", p.failures)
		if s := h.sample(domain, "/api", 60); len(s.instances) != 3 || s.versions["v1"] != s.requests {
			t.Fatalf("D1 after: %s", s)
		}
		comp("api")["instances"] = 3
		_, out = oap("observe", app, "--json")
		var world struct {
			World struct {
				Applications []struct {
					SpecHash string `json:"spec_hash"`
				} `json:"applications"`
			} `json:"world"`
		}
		json.Unmarshal([]byte(out), &world)
		if len(world.World.Applications) != 1 {
			t.Fatalf("D1 observe: %s", out)
		}
		d1 := world.World.Applications[0].SpecHash
		rec.step("cli", "scaleUpTo3", true)

		// E1: a failing migration stops the rollout; the api keeps serving.
		comp("migrate")["env"].(map[string]any)["JOB_EXIT_CODE"] = "7"
		write()
		code, out = oap("app", "apply", file, "--yes", "--events")
		if code == 0 || !strings.Contains(out, "STEP_KIND_RUN_JOB") {
			t.Fatalf("E1 failing migration: %d\n%s", code, out)
		}
		if s := h.sample(domain, "/api", 30); s.failures != 0 || s.versions["v1"] != s.requests {
			t.Fatalf("E1 /api after the failed migration: %s", s)
		}
		_, ex := oap("explain", app)
		rec.step("cli", "failingMigrationBlocksRollout", true)
		rec.observe("E1 explain: %s", strings.TrimSpace(ex))
		comp("migrate")["env"].(map[string]any)["JOB_EXIT_CODE"] = "0"
		write()
		if code, out = oap("app", "apply", file, "--yes"); code != 0 {
			t.Fatalf("E1 restore: %s", out)
		}

		// E2: a broken api release doesn't complete; explain names the api.
		p = h.probe(domain, "/api")
		code, out = oap("app", "deploy", app, "api="+fixtureImage+":bad", "--yes")
		p.end()
		if code == 0 || !strings.Contains(out, "STEP_KIND_AWAIT_READY") || !strings.Contains(out, "api") {
			t.Fatalf("E2 broken release: %d\n%s", code, out)
		}
		rec.step("cli", "brokenReleaseNotCompleted", true)
		rec.step("cli", "outcomeNamesApi", true)
		rec.step("cli", "brokenReleaseApiFailures", p.failures)
		// The failed deploy is undone in the desired state: what runs (v1)
		// is what is desired, and the unready instance is gone.
		if _, ex = oap("explain", app); !strings.Contains(ex, "is as desired") {
			t.Fatalf("E2 explain after the failed deploy:\n%s", ex)
		}
		if s := h.sample(domain, "/api", 30); s.failures != 0 || s.versions["v1"] != s.requests || len(s.instances) != 3 {
			t.Fatalf("E2 /api after: %s", s)
		}
		rec.step("cli", "desiredRestoredAfterFailure", true)

		// E3: correct forward to v2 (api and notes); notes keeps its volume.
		if code, out = oap("app", "deploy", app, "api="+fixtureImage+":v2", "notes="+fixtureImage+":v2", "--yes"); code != 0 {
			t.Fatalf("E3: %s", out)
		}
		if s := h.sample(domain, "/api", 60); len(s.instances) != 3 || s.versions["v2"] != s.requests {
			t.Fatalf("E3 /api: %s", s)
		}
		if c, b := get(domain, "/notes"); c != 200 || !strings.Contains(b, `"boots":2`) {
			t.Fatalf("E3 /notes: %d %s", c, b)
		}
		rec.step("cli", "correctedToV2", true)

		// F: roll back to D1's release: no migration, v1 again, data kept.
		code, out = oap("app", "rollback", app, "--to", d1, "--yes", "--events")
		if code != 0 || strings.Contains(out, "STEP_KIND_RUN_JOB") {
			t.Fatalf("F rollback: %d\n%s", code, out)
		}
		if s := h.sample(domain, "/api", 60); len(s.instances) != 3 || s.versions["v1"] != s.requests {
			t.Fatalf("F /api: %s", s)
		}
		if c, b := get(domain, "/notes"); c != 200 || !strings.Contains(b, `"boots":3`) {
			t.Fatalf("F /notes: %d %s", c, b)
		}
		rec.step("cli", "rollbackSkippedMigration", true)
		rec.step("cli", "volumeDataRetained", true)

		// H: scale down to 1 under traffic: no request fails.
		p = h.probe(domain, "/api")
		if code, out = oap("app", "scale", app, "api", "1", "--yes"); code != 0 {
			t.Fatalf("H scale down: %s", out)
		}
		if p.end(); p.failures != 0 {
			t.Fatalf("H: %s", p)
		}
		rec.step("cli", "scaleDownFailures", p.failures)
	})

	// ---- Connect run: a smoke run over Connect JSON.
	t.Run("connect", func(t *testing.T) {
		const app, domain = "journey-connect", "connect.journey.test"
		tok := signIn("journey-connect", app)
		c := oapv1connect.NewAgentServiceClient(&http.Client{Transport: withToken{tok, http.DefaultTransport}, Timeout: 10 * time.Minute}, h.api, connect.WithProtoJSON())
		ctx := context.Background()
		if _, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{})); err != nil {
			t.Fatalf("S1 observe: %v", err)
		}
		spec := journeyApp(app, domain, 1)
		delete(spec, "api_version")
		delete(spec, "kind")
		delete(spec, "name")
		patch, _ := structpb.NewStruct(jsonRoundTrip(spec))
		p, err := c.Propose(ctx, connect.NewRequest(&oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: app,
			Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: patch}}}}))
		if err != nil || p.Msg.Proposal.Verdict != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
			t.Fatalf("S2 propose: %v %v", p, err)
		}
		a, err := c.Ask(ctx, connect.NewRequest(&oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_APPROVAL, ProposalId: p.Msg.Proposal.Id, Question: "Create journey-connect?"}))
		if err != nil {
			t.Fatal(err)
		}
		answer(a.Msg.AskId)
		ap, err := c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: p.Msg.Proposal.Id}))
		if err != nil {
			t.Fatal(err)
		}
		s, err := c.Await(ctx, connect.NewRequest(&oapv1.AwaitRequest{OperationId: ap.Msg.OperationId, TimeoutSeconds: 600}))
		if err != nil {
			t.Fatal(err)
		}
		job, status := false, oapv1.OutcomeStatus_OUTCOME_STATUS_UNSPECIFIED
		for s.Receive() {
			e := s.Msg().Event
			if e.GetOperationProgress().GetStep().GetKind() == oapv1.StepKind_STEP_KIND_RUN_JOB {
				job = true
			}
			if o := e.GetOutcome(); o != nil {
				status = o.Status
			}
		}
		if !job || status != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
			t.Fatalf("S4 deploy: job %v, outcome %v", job, status)
		}
		if again, err := c.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: p.Msg.Proposal.Id})); err != nil || again.Msg.OperationId != ap.Msg.OperationId {
			t.Fatalf("S4 replay: %v %v", again, err)
		}
		if code, b := get(domain, "/api"); code != 200 || !strings.Contains(b, `"version":"v1"`) {
			t.Fatalf("S4 /api: %d %s", code, b)
		}
		if ex, err := c.Explain(ctx, connect.NewRequest(&oapv1.ExplainRequest{Application: app})); err != nil || len(ex.Msg.Gaps) != 0 {
			t.Fatalf("S5 explain: %v %v", ex, err)
		}
		if _, err := c.Observe(ctx, connect.NewRequest(&oapv1.ObserveRequest{Application: "journey-cli"})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("S6 cross-app: %v", err)
		}
		rec.step("connect", "smoke", "passed")
	})

	// ---- MCP run: a smoke run over MCP.
	t.Run("mcp", func(t *testing.T) {
		const app, domain = "journey-mcp", "mcp.journey.test"
		tok := signIn("journey-mcp", app)
		call := func(method string, params any) map[string]any {
			t.Helper()
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
			req, _ := http.NewRequest(http.MethodPost, h.api+"/mcp", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("Content-Type", "application/json")
			res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var out map[string]any
			json.NewDecoder(res.Body).Decode(&out)
			return out
		}
		tool := func(name string, args any) (map[string]any, bool) {
			t.Helper()
			out := call("tools/call", map[string]any{"name": name, "arguments": args})
			r, _ := out["result"].(map[string]any)
			isErr, _ := r["isError"].(bool)
			sc, _ := r["structuredContent"].(map[string]any)
			return sc, isErr || out["error"] != nil
		}
		if tl := call("tools/list", map[string]any{}); !strings.Contains(fmt.Sprint(tl), "propose") {
			t.Fatalf("S1 tools/list: %v", tl)
		}
		spec := journeyApp(app, domain, 1)
		delete(spec, "api_version")
		delete(spec, "kind")
		delete(spec, "name")
		res, bad := tool("propose", map[string]any{"intent": map[string]any{"application": app, "spec_patch": map[string]any{"merge_patch": spec}}})
		prop, _ := res["proposal"].(map[string]any)
		if bad || prop["verdict"] != "VERDICT_NEEDS_APPROVAL" {
			t.Fatalf("S2 propose: %v", res)
		}
		res, bad = tool("ask", map[string]any{"kind": "ASK_KIND_APPROVAL", "proposal_id": prop["id"], "question": "Create journey-mcp?"})
		if bad {
			t.Fatalf("ask: %v", res)
		}
		answer(res["ask_id"].(string))
		res, bad = tool("apply", map[string]any{"proposal_id": prop["id"]})
		if bad {
			t.Fatalf("S4 apply: %v", res)
		}
		op := res["operation_id"]
		res, _ = tool("await", map[string]any{"operation_id": op, "timeout_seconds": 600})
		b, _ := json.Marshal(res)
		if !strings.Contains(string(b), "STEP_KIND_RUN_JOB") || !strings.Contains(string(b), "OUTCOME_STATUS_ACHIEVED") {
			t.Fatalf("S4 await: %s", b)
		}
		if again, _ := tool("apply", map[string]any{"proposal_id": prop["id"]}); again["operation_id"] != op {
			t.Fatalf("S4 replay: %v", again)
		}
		if code, body := get(domain, "/api"); code != 200 || !strings.Contains(body, `"version":"v1"`) {
			t.Fatalf("S4 /api: %d %s", code, body)
		}
		if ex, _ := tool("explain", map[string]any{"application": app}); ex["gaps"] != nil {
			t.Fatalf("S5 explain: %v", ex)
		}
		if _, denied := tool("observe", map[string]any{"application": "journey-cli"}); !denied {
			t.Fatal("S6: the MCP agent observed another agent's application")
		}
		rec.step("mcp", "smoke", "passed")
	})

	// ---- Final checks.
	t.Run("final", func(t *testing.T) {
		// X1: each app on its own host name; an unknown host gets nothing.
		for app, domain := range map[string]string{"journey-cli": "cli.journey.test", "journey-connect": "connect.journey.test", "journey-mcp": "mcp.journey.test"} {
			if code, b := get(domain, "/"); code != 200 || !strings.Contains(b, app) {
				t.Fatalf("X1 %s: %d %s", domain, code, b)
			}
		}
		if code, b := get("unknown.invalid", "/"); code == 200 && strings.Contains(b, "journey") {
			t.Fatalf("X1: an unknown host was served an app: %d %s", code, b)
		}
		rec.Final["coexistence"] = true

		// X2: oapd stopped: the apps keep serving through the edge; nothing
		// they run is restarted; every interface works again after.
		before := hostSh(`docker ps --format '{{.ID}} {{.Names}}' | grep -v oapd | sort`)
		hostSh(`docker stop oap-journey1-oapd >/dev/null`)
		probes := map[string]*trafficProbe{}
		for _, d := range []string{"cli.journey.test", "connect.journey.test", "mcp.journey.test"} {
			probes[d] = h.probe(d, "/api")
		}
		time.Sleep(30 * time.Second)
		failed := 0
		for d, p := range probes {
			p.end()
			failed += p.failures
			rec.observe("X2 %s while oapd was down: %s", d, p)
		}
		hostSh(`docker start oap-journey1-oapd >/dev/null; i=0; until curl -sf --unix-socket /run/oapd/oapd.sock http://oapd/healthz >/dev/null; do i=$((i+1)); [ $i -gt 60 ] && exit 1; sleep 1; done`)
		after := hostSh(`docker ps --format '{{.ID}} {{.Names}}' | grep -v oapd | sort`)
		rec.Final["outageProbeFailures"] = failed
		if failed != 0 {
			t.Errorf("X2: %d requests failed while oapd was stopped", failed)
		}
		if before != after {
			t.Errorf("X2: containers changed across the outage:\n%s\n---\n%s", before, after)
		}
		for i := 0; i < 50; i++ { // the socket forward reconnects
			if res, err := http.Get(h.api + "/healthz"); err == nil && res.StatusCode == 200 {
				res.Body.Close()
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if code, out := cli(t, dir, map[string]string{"OAP_AGENT": "journey-cli"}, "observe", "journey-cli"); code != 0 {
			t.Errorf("X2: the CLI after oapd's restart: %s", out)
		}
		rec.Final["interfacesAfterRestart"] = true

		// X3: nothing on the 1 GiB host was killed for memory.
		if after := ooms(); after != oomsBefore {
			t.Errorf("X3: %s became %s", oomsBefore, after)
		}
		rec.Final["oomKills"] = ooms()
		rec.Final["memoryPeakMiB"] = hostSh(`echo $(( $(cat /sys/fs/cgroup/memory.peak) / 1048576 ))`)
	})
}

// jsonRoundTrip makes a tree structpb accepts (numbers as float64).
func jsonRoundTrip(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}
