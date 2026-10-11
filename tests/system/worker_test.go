package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNativeDockerWorkerWithWebRestartAndScopedUpdate(t *testing.T) {
	socket, workerImage, webImage := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_WORKER_IMAGE"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || workerImage == "" {
		t.Skip("isolated worker fixture required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	target := domain.Target{ID: "worker-docker", Name: "Worker Docker", Operator: "docker", URL: "unix://" + socket, Environment: "staging", Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	m := domain.Manifest{Name: "worker-" + domain.NewID()[:8], Environment: "staging", TargetID: target.ID, Components: []domain.Component{
		{Name: "worker", Kind: "worker", Image: workerImage, Instances: 1, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}},
		{Name: "web", Kind: "web", Image: webImage, Port: 8080, HostPort: port, Instances: 1, DependsOn: []string{"worker"}},
	}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "worker-web-initial")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	waitRelease(t, c, d.ID)
	workerRef, webRef := domain.ResourceName(app.ID, "worker", 1), domain.ResourceName(app.ID, "web", 1)
	defer adapter.Stop(ctx, workerRef)
	defer adapter.Stop(ctx, webRef)
	worker, err := adapter.Inspect(ctx, workerRef)
	if err != nil {
		t.Fatal(err)
	}
	if worker.URL != "" || worker.Port != 0 || worker.Status != "running:healthy" {
		t.Fatal("worker exposed an HTTP endpoint or failed native readiness")
	}
	logs, err := adapter.Logs(ctx, workerRef, 20)
	if err != nil || !strings.Contains(logs, "worker active boot=") {
		t.Fatal("worker logs unavailable")
	}
	web, _ := adapter.Inspect(ctx, webRef)
	boot := fixtureJSON(t, web.URL)["boot"]
	restart, err := c.EnqueueRestart(ctx, app.ID, "worker-restart", controller.RestartRequest{Component: "worker", Ordinal: 1}, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	waitRelease(t, c, restart.ID)
	if fixtureJSON(t, web.URL)["boot"] != boot {
		t.Fatal("worker restart changed web runtime")
	}
	app.Manifest.Components[0].Env = map[string]string{"WORKER_MODE": "updated"}
	app, err = c.UpdateApplication(ctx, app.ID, app.Manifest, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	update, err := c.EnqueueSelected(ctx, app.ID, "worker-only-update", nil, app.Version, []string{"worker"})
	if err != nil {
		t.Fatal(err)
	}
	waitRelease(t, c, update.ID)
	if fixtureJSON(t, web.URL)["boot"] != boot {
		t.Fatal("worker update changed web runtime")
	}
}
func TestWorkerCapabilityIsExplicit(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	m := manifest()
	m.Components[0].Kind = "worker"
	m.Components[0].Port = 0
	if _, err := c.CreateApplication(context.Background(), m); err == nil || !strings.Contains(err.Error(), "long-running workers") {
		t.Fatal("worker accepted without capability")
	}
}

func TestNativeWorkerLostDispatchResponseRecoveryPreservesProcess(t *testing.T) {
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_WORKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated worker image required")
	}
	ctx := context.Background()
	target := domain.Target{ID: "worker-recovery", Name: "Worker recovery", Operator: "docker", URL: "unix://" + socket, Environment: "staging", Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	native, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	lost := &lostDockerResponse{Client: native}
	c, _ := setup(t, lost)
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	app, err := c.CreateApplication(ctx, domain.Manifest{Name: "worker-recovery", Environment: "staging", TargetID: target.ID, Components: []domain.Component{{Name: "worker", Kind: "worker", Image: image, Instances: 1, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "worker-lost-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4; n++ {
		_ = c.Advance(ctx, d.ID)
	}
	d, err = c.Store.Deployment(ctx, d.ID)
	if err != nil || d.State != "attention" || lost.dispatches != 1 || lost.operation == "" {
		t.Fatal("worker lost dispatch was not fenced")
	}
	ref := d.Steps[0].ResourceID
	defer native.Stop(ctx, ref)
	healthy := false
	for until := time.Now().Add(15 * time.Second); time.Now().Before(until); {
		r, e := native.Inspect(ctx, ref)
		if e == nil && r.Status == "running:healthy" {
			healthy = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("native worker did not become healthy")
	}
	boot := func() string {
		logs, e := native.Logs(ctx, ref, 20)
		if e != nil {
			t.Fatal(e)
		}
		matches := regexp.MustCompile(`boot=([0-9]+)`).FindAllStringSubmatch(logs, -1)
		if len(matches) == 0 {
			t.Fatal("worker boot identity unavailable")
		}
		return matches[len(matches)-1][1]
	}
	before := boot()
	recreated, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recreated.Enqueue(ctx, app.ID, "worker-must-remain-fenced"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("worker uncertainty lost its fence")
	}
	if _, err = recreated.Recover(ctx, d.ID, controller.Recovery{Component: "worker", Ordinal: 1, RemoteDeploymentID: lost.operation, ExpectedUpdatedAt: d.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		_ = recreated.Advance(ctx, d.ID)
	}
	finished, err := c.Store.Deployment(ctx, d.ID)
	if err != nil || finished.State != "succeeded" || lost.dispatches != 1 || boot() != before {
		t.Fatal("worker recovery recreated or restarted its process")
	}
	resources, err := native.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range resources {
		if r.Description == "OpenAppPlatform:"+app.ID+":worker" && strings.HasPrefix(r.Status, "running") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("worker recovery produced duplicate running instances")
	}
}
