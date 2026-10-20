package docker

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

var ctx = context.Background()

// Images the real contract run pulls: two different long-running servers,
// and a job that exits 0.
var images = contract.Images{Web1: "traefik/whoami:v1.10.3", Web2: "traefik/whoami:v1.10.2",
	DB: "traefik/whoami:v1.10.3", Job: "hello-world:latest"}

// Socket returns the Docker socket for real tests, or skips unless
// OAP_TEST_DOCKER=1. OAP_TEST_DOCKER_SOCKET overrides the default socket.
func socket(t *testing.T) string {
	t.Helper()
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("real Docker tests run with OAP_TEST_DOCKER=1")
	}
	if s := os.Getenv("OAP_TEST_DOCKER_SOCKET"); s != "" {
		return s
	}
	return "/var/run/docker.sock"
}

// newRuntime returns a runtime for a fresh installation id, removing
// everything labelled with it when the test ends.
func newRuntime(t *testing.T) *Runtime {
	t.Helper()
	b := make([]byte, 4)
	rand.Read(b)
	r, err := New(socket(t), fmt.Sprintf("test-%x", b))
	if err != nil {
		t.Fatal(err)
	}
	r.StopTimeout = 2 * time.Second
	t.Cleanup(func() {
		if err := r.RemoveAll(context.Background()); err != nil {
			t.Errorf("cleaning up %s: %v", r.Install, err)
		}
	})
	return r
}

func TestRuntimeContract(t *testing.T) {
	socket(t)
	contract.Runtime(t, images, func(t *testing.T) ports.Runtime { return newRuntime(t) }, time.Sleep)
}

// raw creates and starts a container OAP didn't create, with the given
// name and labels, and removes it when the test ends.
func raw(t *testing.T, r *Runtime, name string, labels map[string]string) string {
	t.Helper()
	if err := r.ensureImage(ctx, r.HelperImage); err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"Id"`
	}
	cfg := containerConfig{Image: r.HelperImage, Cmd: []string{"sleep", "300"}, Labels: labels}
	if err := r.c.do(ctx, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &created); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.remove(context.Background(), created.ID) })
	if err := r.c.do(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func running(t *testing.T, r *Runtime, id string) bool {
	t.Helper()
	var in struct{ State struct{ Running bool } }
	if err := r.c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &in); err != nil {
		t.Fatal(err)
	}
	return in.State.Running
}

// TestAdoptsOnlyByLabels: containers without OAP's labels, or with only
// some of them, are never observed, adopted, stopped or removed, even when
// they hold the name OAP would use.
func TestAdoptsOnlyByLabels(t *testing.T) {
	r := newRuntime(t)
	ref := ops.InstanceRef{Application: "labels", Component: "web", Ordinal: 1, Release: "r-1"}
	squatter := raw(t, r, ContainerName(r.Install, ref), nil)
	partial := raw(t, r, "oap-"+r.Install+"-partial", map[string]string{LabelInstall: r.Install, LabelRole: RoleInstance,
		LabelApplication: "labels", LabelComponent: "web", LabelOrdinal: "2"}) // no release
	other := raw(t, r, "oap-"+r.Install+"-other", refLabels(map[string]string{LabelInstall: "someone-else", LabelRole: RoleInstance},
		ops.InstanceRef{Application: "labels", Component: "web", Ordinal: 3, Release: "r-1"}))

	obs, err := r.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Instances) != 0 {
		t.Fatalf("observed containers OAP didn't create: %+v", obs.Instances)
	}
	_, err = r.Start(ctx, ops.InstanceSpec{Ref: ref, Image: images.Web1, Port: 80})
	if err == nil || !strings.Contains(err.Error(), "left alone") {
		t.Fatalf("start over a squatted name: %v", err)
	}
	for _, ref := range []ops.InstanceRef{ref, {Application: "labels", Component: "web", Ordinal: 3, Release: "r-1"}} {
		if err := r.Stop(ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	for name, id := range map[string]string{"unlabelled": squatter, "partly labelled": partial, "other installation": other} {
		if !running(t, r, id) {
			t.Fatalf("the %s container was stopped", name)
		}
	}
}

// TestServiceNamesResolve: another container on the installation's
// network reaches a component by the service name that connection URLs
// use (ops.ServiceURL), whatever its instances' release.
func TestServiceNamesResolve(t *testing.T) {
	r := newRuntime(t)
	ref := ops.InstanceRef{Application: "site", Component: "web", Ordinal: 1, Release: "r-1"}
	if _, err := r.Start(ctx, ops.InstanceSpec{Ref: ref, Image: images.Web1, Port: 80}); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureImage(ctx, r.HelperImage); err != nil {
		t.Fatal(err)
	}
	cfg := containerConfig{Image: r.HelperImage, Labels: map[string]string{LabelInstall: r.Install},
		Cmd: []string{"wget", "-q", "-T", "5", "-O", "-", ops.ServiceURL("site", "web", 80)}}
	cfg.HostConfig.NetworkMode = NetworkName(r.Install)
	var created struct {
		ID string `json:"Id"`
	}
	if err := r.c.do(ctx, http.MethodPost, "/containers/create", nil, cfg, &created); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.remove(context.Background(), created.ID) })
	if err := r.c.do(ctx, http.MethodPost, "/containers/"+created.ID+"/start", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var exit struct{ StatusCode int }
	if err := r.c.do(ctx, http.MethodPost, "/containers/"+created.ID+"/wait", nil, nil, &exit); err != nil {
		t.Fatal(err)
	}
	if exit.StatusCode != 0 {
		t.Fatalf("%s didn't answer from another container on the network (exit %d)", ops.ServiceURL("site", "web", 80), exit.StatusCode)
	}
}
