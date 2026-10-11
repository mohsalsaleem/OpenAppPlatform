package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"net"
	"os"
	"testing"
)

func TestDockerRuntimeSecretReferenceAndControllerRecreation(t *testing.T) {
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated Docker fixtures required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	target := domain.Target{ID: "secret-docker", Name: "Secret Docker", Operator: "docker", URL: "unix://" + socket, Environment: "staging", Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	m := domain.Manifest{Name: "secret-" + domain.NewID()[:8], TargetID: target.ID, Environment: "staging", Components: []domain.Component{{Name: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, SecretRefs: map[string]string{"OAP_TEST_MESSAGE": "message-v1"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}}}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]runtimeinputs.Binding{{Reference: "message-v1", TargetID: target.ID, ApplicationID: app.ID, Component: "web", Variable: "OAP_TEST_MESSAGE", ValueEnv: "OAP_SECRET_MESSAGE_V1"}})
	c.RuntimeInputs, err = runtimeinputs.Parse(raw, func(string) string { return "docker-private-fixture" })
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "docker-secret-deployment")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	restarted.RuntimeInputs = c.RuntimeInputs
	c = restarted
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	waitRelease(t, c, d.ID)
	ref := domain.ResourceName(app.ID, "web", 1)
	defer adapter.Stop(ctx, ref)
	resource, err := adapter.Inspect(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if fixtureJSON(t, resource.URL)["message"] != "docker-private-fixture" {
		t.Fatal("native runtime did not receive the secret input")
	}
	saved, _ := c.Store.Application(ctx, app.ID)
	if saved.Manifest.Components[0].Env["OAP_TEST_MESSAGE"] != "" {
		t.Fatal("plaintext secret saved in definition")
	}
	logs, err := c.RedactRuntimeLogs(ctx, app.ID, "docker-private-fixture")
	if err != nil || logs != "[REDACTED]" {
		t.Fatal("native deployment redaction failed")
	}
}
