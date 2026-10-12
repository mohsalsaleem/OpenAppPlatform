package system_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
)

func TestNativeNamedVolumePersistenceAndLimitUpdates(t *testing.T) {
	socket, image, replacement := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE"), os.Getenv("OAP_TEST_DOCKER_REPLACEMENT_IMAGE")
	if socket == "" || image == "" || replacement == "" {
		t.Skip("isolated Docker fixtures required")
	}
	ctx := context.Background()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	target := domain.Target{ID: "volume-fixture", Name: "Volume Fixture", Operator: "docker", URL: "unix://" + socket, Environment: "staging"}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := setup(t, adapter)
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	manifest := domain.Manifest{Name: "volume-" + domain.NewID()[:8], Environment: "staging", TargetID: target.ID, Components: []domain.Component{{Name: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, Env: map[string]string{"FIXTURE_VOLUME": "enabled"}, Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}, Limits: &domain.ResourceLimits{MemoryMiB: 128, CPUMillis: 250}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}}}
	app, err := c.CreateApplication(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	ref := domain.ResourceName(app.ID, "web", 1)
	defer adapter.Stop(ctx, ref)
	deploy := func(key string) {
		t.Helper()
		d, e := c.Enqueue(ctx, app.ID, key)
		if e != nil {
			t.Fatal(e)
		}
		waitRelease(t, c, d.ID)
	}
	deploy("volume-first")
	resource, err := adapter.Inspect(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(resource.URL+"/volume", "text/plain", strings.NewReader("retained-through-releases"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("non-root volume write failed")
	}
	checkData := func() {
		t.Helper()
		r, e := http.Get(resource.URL + "/volume")
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 || string(b) != "retained-through-releases" {
			t.Fatal("persistent marker lost")
		}
	}
	if _, err = adapter.Restart(ctx, ref); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		v, e := adapter.Inspect(ctx, ref)
		if e == nil && controller.Healthy(v.Status) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	checkData()
	next := app.Manifest
	next.Components = append([]domain.Component(nil), next.Components...)
	next.Components[0].Image = replacement
	next.Components[0].Port = 8025
	next.Components[0].Limits = &domain.ResourceLimits{MemoryMiB: 192, CPUMillis: 500}
	app, err = c.UpdateApplication(ctx, app.ID, next, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	deploy("volume-replacement")
	checkData()
	engine := http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	req, _ := http.NewRequest("GET", "http://docker/v1.45/containers/"+ref+"/json", nil)
	r, err := engine.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var native struct {
		Config     struct{ User string }
		HostConfig struct{ Memory, NanoCpus int64 }
		Mounts     []struct {
			Type, Name, Destination string
			RW                      bool
		}
	}
	if err = json.NewDecoder(r.Body).Decode(&native); err != nil {
		t.Fatal(err)
	}
	if native.Config.User != "fixture" || native.HostConfig.Memory != 192<<20 || native.HostConfig.NanoCpus != 500000000 || len(native.Mounts) != 1 || native.Mounts[0].Type != "volume" || native.Mounts[0].Destination != "/data" {
		t.Fatalf("native limits/non-root mount mismatch: %+v", native)
	}
	volumeName := native.Mounts[0].Name
	stale := app.Manifest.Components[0]
	stale.Volumes = nil
	if _, err := adapter.Ensure(ctx, operator.Spec{Name: ref, Ownership: "OpenAppPlatform:" + app.ID + ":web", Component: stale}); err == nil {
		t.Fatal("stale definition unmounted persistent data")
	}
	checkData()
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), changed.Components...)
	changed.Components[0].Volumes = nil
	if _, err = c.UpdateApplication(ctx, app.ID, changed, app.Version); err == nil {
		t.Fatal("saved mount removal accepted")
	}
	changed.Components[0].Volumes = app.Manifest.Components[0].Volumes
	changed.Components[0].Instances = 2
	if _, err = c.UpdateApplication(ctx, app.ID, changed, app.Version); err == nil {
		t.Fatal("persistent writer scaled")
	}
	// Recreate the controller and adapter to prove names are not in-memory state.
	c.Jobs.Stop(ctx)
	adapter, err = docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	c, err = controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	deploy("volume-controller-recreated")
	checkData()
	req, _ = http.NewRequest("GET", "http://docker/v1.45/volumes/"+volumeName, nil)
	r, err = engine.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("retained volume missing")
	}
	// The isolated harness owns final cleanup, including this test-only volume.
}
