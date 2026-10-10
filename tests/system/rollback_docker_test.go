package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"net"
	"os"
	"testing"
)

func TestNativeDockerImageRollbackPreservesBindingAndConfiguration(t *testing.T) {
	socket, oldTag, newTag := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE"), os.Getenv("OAP_TEST_DOCKER_REPLACEMENT_IMAGE")
	if socket == "" || oldTag == "" || newTag == "" {
		t.Skip("isolated Docker images required")
	}
	ctx := context.Background()
	target := domain.Target{ID: "rollback-docker", Name: "Rollback Docker", Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	oldImage, err := adapter.CachedImageID(ctx, oldTag)
	if err != nil {
		t.Fatal(err)
	}
	newImage, err := adapter.CachedImageID(ctx, newTag)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	c, _ := setup(t, adapter)
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	// Both fixture binaries support a chosen listening port, allowing configuration
	// to stay identical while content addresses and response versions change.
	m := domain.Manifest{Name: "rollback-native-" + domain.NewID()[:8], TargetID: target.ID, Environment: "staging", Components: []domain.Component{{Name: "web", Image: oldImage, Port: 8080, HostPort: port, Instances: 1, Env: map[string]string{"FIXTURE_PORT": "8080"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}}}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	old, err := c.Enqueue(ctx, app.ID, "native-rollback-old")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	old = waitRelease(t, c, old.ID)
	defer adapter.Stop(ctx, old.Steps[0].ResourceID)
	// The fixture's image HEALTHCHECK port follows FIXTURE_PORT at image build time;
	// the environment selects that same port for both binaries.
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Image = newImage
	app, err = c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := c.Enqueue(ctx, app.ID, "native-rollback-new")
	if err != nil {
		t.Fatal(err)
	}
	newer = waitRelease(t, c, newer.ID)
	plan, err := c.PlanRollback(ctx, app.ID, old.ID, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := c.EnqueueRollback(ctx, app.ID, "native-rollback-apply", controller.RollbackRequest{ReleaseID: old.ID, PlanHash: plan.PlanHash, AcknowledgeDataCompatibility: true}, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	restored = waitRelease(t, c, restored.ID)
	if restored.Steps[0].ResourceID != old.Steps[0].ResourceID || restored.Steps[0].RollbackFrom != old.ID {
		t.Fatal("rollback lost stable binding or provenance")
	}
	runtime, err := adapter.Inspect(ctx, restored.Steps[0].ResourceID)
	if err != nil || runtime.Image != oldImage || runtime.Status != "running:healthy" {
		t.Fatal("native rollback failed", err)
	}
	response := fixtureJSON(t, runtime.URL)
	if response["version"] != "v1" {
		t.Fatal("old native fixture version was not restored")
	}
}
