package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// journeyHost is a Docker-in-Docker host running the real oapd and Caddy.
// oapd's socket is reachable at api (as an SSH forward would make it),
// and Caddy's port 80 at web.
type journeyHost struct {
	name, api, web string
}

func newJourneyHost(t *testing.T, memory ...string) *journeyHost {
	t.Helper()
	run := func(stdin []byte, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	const image = "oapd:journey-test"
	buildOapdImage(t, image)

	h := &journeyHost{name: fmt.Sprintf("oap-journey-%d", time.Now().UnixNano())}
	run(nil, "env", "DOCKER_CONFIG="+t.TempDir(), "docker", "pull", "-q", "docker:27.5.1-dind")
	runArgs := []string{"run", "-d", "--privileged", "--name", h.name, "-p", "127.0.0.1::80", "-p", "127.0.0.1::7000"}
	if len(memory) > 0 { // the host's whole memory, swap included
		runArgs = append(runArgs, "--memory", memory[0], "--memory-swap", memory[0])
	}
	run(nil, "docker", append(runArgs, "docker:27.5.1-dind")...)
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", "-v", h.name).Run() })
	sh := func(script string) string {
		t.Helper()
		return run([]byte(script), "docker", "exec", "-i", h.name, "sh", "-s")
	}
	sh(`i=0; until docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -gt 60 ] && exit 1; sleep 1; done
apk add -q --no-cache curl socat`)
	load, _ := host.LoadImageCommand(image)
	run([]byte(run(nil, "docker", "save", image)), "docker", "exec", "-i", h.name, "sh", "-c", load)
	script, err := host.Install{ID: "journey1", Oapd: image, Caddy: host.CaddyImage}.Script()
	if err != nil {
		t.Fatal(err)
	}
	sh(script)
	// What an SSH forward does for oapctl: oapd's socket on a local port.
	run(nil, "docker", "exec", "-d", h.name, "socat", "TCP-LISTEN:7000,fork,reuseaddr", "UNIX-CONNECT:"+host.OapdSocket)
	port := func(p string) string {
		out := strings.TrimSpace(run(nil, "docker", "port", h.name, p))
		return "http://127.0.0.1:" + out[strings.LastIndex(out, ":")+1:]
	}
	h.api, h.web = port("7000"), port("80")
	for i := 0; i < 50; i++ {
		if res, err := http.Get(h.api + "/healthz"); err == nil && res.StatusCode == 200 {
			res.Body.Close()
			return h
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("oapd's socket never answered through the forward")
	return nil
}

// ps lists the host's containers with their state, for failure messages.
func (h *journeyHost) ps() string {
	out, _ := exec.Command("docker", "exec", h.name, "docker", "ps", "-a", "--format", "{{.Names}} {{.Image}} {{.Status}}").CombinedOutput()
	return string(out)
}

// buildOapdImage builds oapd's image for this machine's architecture,
// with the first-party extensions, as make oapd-image does for amd64.
func buildOapdImage(t *testing.T, image string) {
	t.Helper()
	ctxDir := t.TempDir()
	for bin, pkg := range map[string]string{"oapd": "../oapd", "oap-ext-builder": "../oap-ext-builder", "oap-ext-registry": "../oap-ext-registry", "oap-ext-github": "../oap-ext-github"} {
		build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(ctxDir, bin+"-linux-"+runtime.GOARCH), pkg)
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", bin, err, out)
		}
	}
	df, _ := os.ReadFile("../oapd/Dockerfile")
	os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), df, 0o644)
	if out, err := exec.Command("docker", "build", "-q", "-t", image, ctxDir).CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "image", "rm", image).Run() })
}

// get fetches / from an application's domain through Caddy.
func (h *journeyHost) get(t *testing.T, domain string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.web+"/", nil)
	req.Host = domain
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// serving waits until the domain answers 200 through Caddy with body text.
func (h *journeyHost) serving(t *testing.T, domain, text string) {
	t.Helper()
	var code int
	var body string
	for i := 0; i < 60; i++ {
		if code, body = h.get(t, domain); code == 200 && strings.Contains(body, text) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s isn't serving %q: %d %s", domain, text, code, body)
}

func journeySpec(app string) map[string]any {
	return map[string]any{
		"environment": "production",
		"ingress":     map[string]any{"domain": app + ".journey.test", "routes": map[string]any{"/": map[string]any{"component": "web"}}},
		"components": map[string]any{
			"migrate": map[string]any{"kind": "job", "image": "hello-world:latest"},
			"web": map[string]any{"kind": "web", "image": webV1, "port": 80, "after": []any{"migrate"},
				"health": map[string]any{"mode": "http", "path": "/"}, "readiness": map[string]any{"timeout_seconds": 30}},
		},
	}
}

// Images for the journey: three good web releases (Alpine-based, so the
// in-container HTTP health check has wget), a second migration, and a web
// image that never serves (busybox exits at once).
const (
	webV1     = "caddy:2.10.2-alpine"
	webV2     = "caddy:2.10.0-alpine"
	webV3     = "caddy:2.9.1-alpine"
	brokenWeb = "busybox:1.37"
	migrate2  = "busybox:1.37"
)

// TestPhase3Journey is phase 3's exit: on a host with the real oapd, Caddy
// and Docker, one application is created (its migration job first),
// deployed with a new migration, scaled, broken by a bad image, explained,
// fixed, rolled back and verified, driven only through MCP; and another
// the same way only through the CLI. Gated on OAP_TEST_DOCKER=1.
func TestPhase3Journey(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	h := newJourneyHost(t)
	t.Run("mcp", func(t *testing.T) { mcpJourney(t, h) })
	t.Run("cli", func(t *testing.T) { cliJourney(t, h) })
}

// mcpCall runs one MCP tool and returns its structured result; isError
// results fail the test unless allowed.
func mcpCall(t *testing.T, h *journeyHost, tool string, args any, allowError bool) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Post(h.api+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.Error != nil {
		t.Fatalf("%s: %v %v", tool, err, out.Error)
	}
	if out.Result.IsError && !allowError {
		t.Fatalf("%s failed: %+v", tool, out.Result.Content)
	}
	if out.Result.StructuredContent == nil && len(out.Result.Content) > 0 {
		json.Unmarshal([]byte(out.Result.Content[0].Text), &out.Result.StructuredContent)
	}
	return out.Result.StructuredContent
}

// mcpChange proposes an intent, applies it and awaits the outcome. It
// returns the outcome status, the proposal and the operation's events.
func mcpChange(t *testing.T, h *journeyHost, intent map[string]any) (string, map[string]any, string) {
	t.Helper()
	p := mcpCall(t, h, "propose", map[string]any{"intent": intent}, false)["proposal"].(map[string]any)
	// The owner drives this journey and applies sensitive changes (a new
	// domain) itself; only a denial stops it.
	if p["verdict"] == "VERDICT_DENIED" {
		t.Fatalf("verdict %v: %v", p["verdict"], p["reasons"])
	}
	op := mcpCall(t, h, "apply", map[string]any{"proposal_id": p["id"]}, false)["operation_id"].(string)
	evs := mcpCall(t, h, "await", map[string]any{"operation_id": op, "timeout_seconds": 300}, false)
	b, _ := json.Marshal(evs)
	status := ""
	var all struct {
		Events []struct {
			Outcome struct {
				Status string `json:"status"`
			} `json:"outcome"`
		} `json:"events"`
	}
	json.Unmarshal(b, &all)
	for _, e := range all.Events {
		if e.Outcome.Status != "" {
			status = e.Outcome.Status
		}
	}
	return status, p, string(b)
}

func webState(t *testing.T, world map[string]any, app string) (converged bool, instances float64, image string) {
	t.Helper()
	b, _ := json.Marshal(world)
	var w struct {
		World struct {
			Applications []struct {
				Name       string `json:"name"`
				Components []struct {
					Name      string `json:"name"`
					Converged bool   `json:"converged"`
					Actual    struct {
						Instances float64 `json:"instances"`
						Image     string  `json:"image"`
					} `json:"actual"`
				} `json:"components"`
			} `json:"applications"`
		} `json:"world"`
	}
	json.Unmarshal(b, &w)
	for _, a := range w.World.Applications {
		if a.Name != app {
			continue
		}
		for _, c := range a.Components {
			if c.Name == "web" {
				return c.Converged, c.Actual.Instances, c.Actual.Image
			}
		}
	}
	return false, 0, ""
}

func mcpJourney(t *testing.T, h *journeyHost) {
	const app = "viamcp"
	domain := app + ".journey.test"
	achieved := "OUTCOME_STATUS_ACHIEVED"

	// Create: the migration job runs, then the web component.
	if st, _, evs := mcpChange(t, h, map[string]any{"application": app, "spec_patch": map[string]any{"merge_patch": journeySpec(app)}}); st != achieved || !strings.Contains(evs, "migrate") {
		t.Fatalf("create: %s\n%s", st, evs)
	}
	h.serving(t, domain, "Caddy")

	// Deploy with a migration: a new job image runs before the new web.
	if st, _, evs := mcpChange(t, h, map[string]any{"application": app, "deploy": map[string]any{"images": map[string]any{"web": webV2, "migrate": migrate2}}}); st != achieved || !strings.Contains(evs, "STEP_KIND_RUN_JOB") {
		t.Fatalf("deploy with a migration: %s\n%s", st, evs)
	}

	// Scale.
	if st, _, _ := mcpChange(t, h, map[string]any{"application": app, "scale": map[string]any{"component": "web", "instances": 3}}); st != achieved {
		t.Fatalf("scale: %s", st)
	}
	if conv, n, _ := webState(t, mcpCall(t, h, "observe", map[string]any{"application": app, "detail": true}, false), app); !conv || n != 3 {
		t.Fatalf("after scaling: converged %v, %v instances", conv, n)
	}

	// Injected failure: an image that never serves.
	st, broken, _ := mcpChange(t, h, map[string]any{"application": app, "deploy": map[string]any{"images": map[string]any{"web": brokenWeb}}})
	if st == achieved {
		t.Fatal("a web image that never serves was reported achieved")
	}
	// Explain names the gap.
	ex := mcpCall(t, h, "explain", map[string]any{"application": app}, false)
	if gaps, _ := ex["gaps"].([]any); len(gaps) == 0 {
		t.Fatalf("explain found nothing after the failure: %v", ex)
	}

	// Fix: forward to a good image.
	if st, _, _ := mcpChange(t, h, map[string]any{"application": app, "deploy": map[string]any{"images": map[string]any{"web": webV3}}}); st != achieved {
		t.Fatalf("fix: %s", st)
	}
	h.serving(t, domain, "Caddy")

	// Roll back to the release before the failure, with the broken
	// deploy's undo (a rollback to the spec hash it started from).
	undo, _ := broken["undo"].(map[string]any)
	if undo == nil || undo["rollback"] == nil {
		t.Fatalf("the failed deploy had no rollback undo: %v", broken["undo"])
	}
	if st, _, _ := mcpChange(t, h, undo); st != achieved {
		t.Fatalf("roll back: %s", st)
	}

	// Verify: converged on the earlier image with three instances, serving.
	world := mcpCall(t, h, "observe", map[string]any{"application": app, "detail": true}, false)
	conv, n, img := webState(t, world, app)
	if !conv || n != 3 || img != webV2 {
		eb, _ := json.Marshal(mcpCall(t, h, "explain", map[string]any{"application": app}, false))
		t.Fatalf("after rolling back: converged %v, %v instances, image %s\nexplain: %s\n%s", conv, n, img, eb, h.ps())
	}
	if ex := mcpCall(t, h, "explain", map[string]any{"application": app}, false); ex["gaps"] != nil {
		t.Fatalf("explain still finds gaps: %v", ex)
	}
	h.serving(t, domain, "Caddy")
}

func cliJourney(t *testing.T, h *journeyHost) {
	const app = "viacli"
	domain := app + ".journey.test"
	dir := t.TempDir()
	env := map[string]string{"OAP_AGENT_URL": h.api}
	oap := func(args ...string) (int, string) {
		t.Helper()
		return cli(t, dir, env, args...)
	}
	must := func(args ...string) string {
		t.Helper()
		code, out := oap(args...)
		if code != 0 {
			t.Fatalf("oapctl %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
		return out
	}

	spec := journeySpec(app)
	spec["api_version"], spec["kind"], spec["name"] = "oap.dev/v1", "Application", app
	b, _ := json.Marshal(spec) // JSON is YAML
	file := filepath.Join(t.TempDir(), "oap.yaml")
	os.WriteFile(file, b, 0o600)

	must("app", "apply", file, "--yes")
	h.serving(t, domain, "Caddy")
	if out := must("app", "deploy", app, "web="+webV2, "migrate="+migrate2, "--yes", "--events"); !strings.Contains(out, "STEP_KIND_RUN_JOB") {
		t.Fatalf("deploy with a migration ran no job:\n%s", out)
	}
	must("app", "scale", app, "web", "3", "--yes")
	if out := must("observe", app); !strings.Contains(out, "3/3 running") {
		t.Fatalf("after scaling:\n%s", out)
	}

	code, out := oap("app", "deploy", app, "web="+brokenWeb, "--yes")
	if code == 0 {
		t.Fatalf("a web image that never serves was reported deployed:\n%s", out)
	}
	m := strings.Fields(out[strings.Index(out, "app rollback"):])
	if len(m) < 4 || m[2] != app || m[3] != "--to" {
		t.Fatalf("the failure didn't say how to roll back:\n%s", out)
	}
	hash := m[4]
	if out := must("explain", app); !strings.Contains(out, "differs from desired") {
		t.Fatalf("explain after the failure:\n%s", out)
	}

	must("app", "deploy", app, "web="+webV3, "--yes")
	h.serving(t, domain, "Caddy")
	must("app", "rollback", app, "--to", hash, "--yes")

	out = must("observe", app, "--json")
	var world map[string]any
	json.Unmarshal([]byte(out), &world)
	if conv, n, img := webState(t, world, app); !conv || n != 3 || img != webV2 {
		t.Fatalf("after rolling back: converged %v, %v instances, image %s", conv, n, img)
	}
	if out := must("explain", app); !strings.Contains(out, "is as desired") {
		t.Fatalf("explain after rolling back:\n%s", out)
	}
	h.serving(t, domain, "Caddy")
}
