package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
)

// Lose only the response: the real Docker operation has already completed.
type lostDockerResponse struct {
	*docker.Client
	operation  string
	dispatches int
}

func (a *lostDockerResponse) Deploy(ctx context.Context, id string) (string, error) {
	a.dispatches++
	remote, err := a.Client.Deploy(ctx, id)
	if err != nil {
		return remote, err
	}
	a.operation = remote
	return "", errors.New("injected response loss after Docker dispatch")
}

func TestNativeDockerLostResponseRecoveryDoesNotRestartRuntime(t *testing.T) {
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated Docker fixtures required")
	}
	ctx := context.Background()
	target := domain.Target{ID: "recovery-docker", Name: "Recovery Docker", Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
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
	lost := &lostDockerResponse{Client: adapter}
	c, _ := setup(t, lost)
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	app, err := c.CreateApplication(ctx, domain.Manifest{Name: "recovery-native-" + domain.NewID()[:8], TargetID: target.ID, Environment: "staging", Components: []domain.Component{{Name: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, Env: map[string]string{"FIXTURE_PORT": "8080"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "lost-response")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_ = c.Advance(ctx, d.ID)
		d, err = c.Store.Deployment(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.State == "attention" {
			break
		}
	}
	if d.State != "attention" || lost.dispatches != 1 || lost.operation == "" || d.Steps[0].RemoteDeploymentID != "" {
		t.Fatalf("response loss not durably fenced: %+v dispatches=%d", d, lost.dispatches)
	}
	defer adapter.Stop(ctx, d.Steps[0].ResourceID)
	deadline := time.Now().Add(30 * time.Second)
	var endpoint string
	for time.Now().Before(deadline) {
		r, e := adapter.Inspect(ctx, d.Steps[0].ResourceID)
		if e == nil && r.Status == "running:healthy" {
			endpoint = r.URL
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("native fixture did not become healthy")
	}
	before := fixtureJSON(t, endpoint)
	c2, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if err = c2.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Enqueue(ctx, app.ID, "must-stay-fenced"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("uncertain work lost its fence: %v", err)
	}
	request := controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: d.UpdatedAt, RetryPreparation: true}
	if _, err = c2.Recover(ctx, d.ID, request); err == nil {
		t.Fatal("uncertain dispatch accepted as preparation retry")
	}
	request.RetryPreparation = false
	request.RemoteDeploymentID = lost.operation
	recovered, err := c2.Recover(ctx, d.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Recover(ctx, d.ID, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale recovery accepted: %v", err)
	}
	if err = c2.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	finished, err := c.Store.Deployment(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := fixtureJSON(t, endpoint)
	if finished.State != "succeeded" || recovered.Steps[0].ResourceID != d.Steps[0].ResourceID || lost.dispatches != 1 || before["boot"] == "" || before["boot"] != after["boot"] {
		t.Fatalf("recovery replaced or restarted runtime: state=%s dispatches=%d before=%v after=%v", finished.State, lost.dispatches, before, after)
	}
}
