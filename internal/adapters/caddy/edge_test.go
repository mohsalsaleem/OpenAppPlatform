package caddy

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

var ctx = context.Background()

// Image is the Caddy release OAP runs, the one on the v0.1 VPS.
const image = "caddy:2.10.2-alpine"

func gate(t *testing.T) string {
	t.Helper()
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("real Caddy tests run with OAP_TEST_DOCKER=1")
	}
	if s := os.Getenv("OAP_TEST_DOCKER_SOCKET"); s != "" {
		return s
	}
	return "/var/run/docker.sock"
}

func install() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("test-%x", b)
}

func dockerCLI(t *testing.T, args ...string) string {
	t.Helper()
	var stderr strings.Builder
	cmd := exec.Command("docker", args...)
	cmd.Stderr = &stderr // pull progress, kept out of the output
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// bootstrap is the configuration Caddy starts with: only its admin
// endpoint, reachable from the test through a published port.
const bootstrap = `{"admin":{"listen":"0.0.0.0:2019","origins":["localhost:2019"]}}`

// caddy runs a Caddy container labelled for the installation, on its
// network if one is given, and returns the host addresses of its admin API
// and HTTP port. The container is removed when the test ends.
func caddy(t *testing.T, inst, network string) (admin, web string) {
	t.Helper()
	args := []string{"run", "-d", "--name", "oap-" + inst + "-caddy",
		"--label", docker.LabelInstall + "=" + inst, "--label", docker.LabelRole + "=" + docker.RoleHelper,
		"-p", "127.0.0.1::2019", "-p", "127.0.0.1::80", "--entrypoint", "sh"}
	if network != "" {
		args = append(args, "--network", network)
	}
	args = append(args, image, "-c", "printf '%s' '"+bootstrap+"' > /etc/caddy/oap.json && exec caddy run --config /etc/caddy/oap.json")
	id := dockerCLI(t, args...)
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
	admin = dockerCLI(t, "port", id, "2019/tcp")
	web = dockerCLI(t, "port", id, "80/tcp")
	admin, web = strings.Fields(admin)[0], strings.Fields(web)[0]
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if _, _, err := TCP(admin, nil).Config(ctx); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("caddy admin not ready: %v\n%s", err, dockerCLI(t, "logs", id))
		}
	}
	return admin, web
}

func TestEdgeContract(t *testing.T) {
	gate(t)
	contract.Edge(t, func(t *testing.T) ports.Edge {
		inst := install()
		admin, _ := caddy(t, inst, "")
		return TCP(admin, func(r ops.InstanceRef) string { return docker.ContainerName(inst, r) })
	})
}

// TestTrafficAcrossReload sends traffic through a real Caddy to real
// instances: routes work, InFlight counts a slow request, and the request
// survives a reload that removes its instance.
func TestTrafficAcrossReload(t *testing.T) {
	rt, err := docker.New(gate(t), install())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.RemoveAll(context.Background()) })
	r1 := ops.InstanceRef{Application: "edge", Component: "web", Ordinal: 1, Release: "r-1"}
	r2 := ops.InstanceRef{Application: "edge", Component: "web", Ordinal: 2, Release: "r-1"}
	for _, r := range []ops.InstanceRef{r1, r2} {
		if _, err := rt.Start(ctx, ops.InstanceSpec{Ref: r, Image: "traefik/whoami:v1.10.3", Port: 80}); err != nil {
			t.Fatal(err)
		}
	}
	admin, web := caddy(t, rt.Install, docker.NetworkName(rt.Install))
	e := TCP(admin, func(r ops.InstanceRef) string { return docker.ContainerName(rt.Install, r) })
	member := func(r ops.InstanceRef, prefix string, strip bool) ops.RouteMember {
		return ops.RouteMember{Instance: r, Host: "edge.test", PathPrefix: prefix, StripPrefix: strip, Port: 80}
	}
	get := func(path string) (int, string, error) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+web+path, nil)
		req.Host = "edge.test"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), nil
	}

	if _, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member(r1, "/", false), member(r1, "/api", true)}}); err != nil {
		t.Fatal(err)
	}
	// whoami echoes the request line, so the path shows whether the prefix was stripped.
	if code, body, err := get("/api/items"); err != nil || code != 200 || !strings.Contains(body, "GET /items ") {
		t.Fatalf("stripped route: %d %v\n%s", code, err, body)
	}
	if code, body, err := get("/apiary"); err != nil || code != 200 || !strings.Contains(body, "GET /apiary ") {
		t.Fatalf("/apiary must not match /api: %d %v\n%s", code, err, body)
	}

	done := make(chan error, 1)
	go func() {
		code, _, err := get("/slow?wait=3s")
		if err == nil && code != 200 {
			err = fmt.Errorf("status %d", code)
		}
		done <- err
	}()
	time.Sleep(time.Second)
	before, err := e.InFlight(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	if before != 1 {
		t.Fatalf("in flight on %s before reload: %d, want 1", r1.Component, before)
	}
	g, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member(r2, "/", false)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Confirmed(ctx, g); err != nil {
		t.Fatal(err)
	}
	after, err := e.InFlight(ctx, r1)
	if err != nil {
		t.Fatal(err)
	}
	reqErr := <-done
	t.Logf("slow request across a reload that removed its instance: in flight %d before, %d after; result: %v", before, after, reqErr)
	if reqErr != nil {
		t.Fatalf("the in-flight request failed across the reload: %v", reqErr)
	}
	if after != 1 {
		t.Fatalf("in flight on the removed instance after reload: %d, want 1 until it finishes", after)
	}
	if n, _ := e.InFlight(ctx, r1); n != 0 {
		t.Fatalf("in flight after the request finished: %d", n)
	}
	if cfg, _, _ := e.Config(ctx); len(cfg.Members) != 1 || cfg.Members[0] != member(r2, "/", false) {
		t.Fatalf("config lists parked upstreams: %+v", cfg.Members)
	}
	parked := func() bool {
		var whole map[string]any
		if err := e.call(ctx, http.MethodGet, "/config/", nil, &whole); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(fmt.Sprint(whole), parkHost)
	}
	if !parked() {
		t.Fatal("the removed instance was not parked")
	}
	if _, err := e.Apply(ctx, ops.EdgeConfig{Members: []ops.RouteMember{member(r2, "/", false), member(r2, "/v2", false)}}); err != nil {
		t.Fatal(err)
	}
	if parked() {
		t.Fatal("an idle parked upstream was kept")
	}
}
