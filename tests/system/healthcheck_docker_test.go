package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"net"
	"os"
	"testing"
	"time"
)

func TestNativeDockerHTTPProbeOverridesImageFailsAndReturnsToImageCheck(t *testing.T) {
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated Docker fixture required")
	}
	ctx := context.Background()
	target := domain.Target{ID: "http-probes", Name: "HTTP Probes", Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s := testStore(t)
	if err = s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	c, err := controller.New(ctx, s, func(domain.Target) (operator.Adapter, error) { return adapter, nil })
	if err != nil {
		t.Fatal(err)
	}
	c.PollInterval = 20 * time.Millisecond
	m := domain.Manifest{Name: "native-http-probes", Environment: "staging", TargetID: target.ID, Components: []domain.Component{{Name: "web", Kind: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, Strategy: "standard", HealthCheck: &domain.HealthCheck{Mode: "http", Path: "/health/custom", IntervalSeconds: 1, TimeoutSeconds: 1, Retries: 1}}}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	release, err := c.Enqueue(ctx, app.ID, "http-custom-ok")
	if err != nil {
		t.Fatal(err)
	}
	release = waitRelease(t, c, release.ID)
	defer adapter.Stop(ctx, release.Steps[0].ResourceID)
	if err = adapter.CheckHealthCheck(ctx, release.Steps[0].ResourceID, release.Manifest.Components[0], false); err != nil {
		t.Fatal(err)
	}
	// Only the check changes. The image's own check stays healthy, so failure proves
	// the configured endpoint overrides it rather than merely inheriting image health.
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	copyProbe := *changed.Components[0].HealthCheck
	copyProbe.Path = "/health/fail"
	changed.Components[0].HealthCheck = &copyProbe
	app, err = c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	broken, err := c.Enqueue(ctx, app.ID, "http-custom-fail")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		broken, _ = c.Store.Deployment(ctx, broken.ID)
		if domain.Terminal(broken.State) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if broken.State != "failed" || broken.Steps[0].RemoteDeploymentID == release.Steps[0].RemoteDeploymentID {
		t.Fatalf("HTTP failure was not real runtime replacement: %+v", broken)
	}
	if err = adapter.CheckHealthCheck(ctx, broken.Steps[0].ResourceID, broken.Manifest.Components[0], false); err != nil {
		t.Fatal(err)
	}
	changed = app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].HealthCheck = &domain.HealthCheck{Mode: "image"}
	app, err = c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := c.Enqueue(ctx, app.ID, "http-image-return")
	if err != nil {
		t.Fatal(err)
	}
	restored = waitRelease(t, c, restored.ID)
	if err = adapter.CheckHealthCheck(ctx, restored.Steps[0].ResourceID, restored.Manifest.Components[0], false); err != nil {
		t.Fatal(err)
	}
	if restored.Steps[0].ResourceID != release.Steps[0].ResourceID {
		t.Fatal("stable binding changed")
	}
}
