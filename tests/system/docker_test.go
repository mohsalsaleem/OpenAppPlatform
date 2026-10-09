package system_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
)

func TestLiveDockerLifecycleOffline(t *testing.T) {
	socket := os.Getenv("OAP_TEST_DOCKER_SOCKET")
	if socket == "" {
		t.Skip("set OAP_TEST_DOCKER_SOCKET for local Docker integration")
	}
	image := os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if image == "" {
		image = "oap-hello-api:dev"
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	target := domain.Target{ID: "docker-fixture", Name: "Docker local staging", Operator: "docker", URL: "unix://" + socket, Environment: "staging", Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	a, e := docker.New(target)
	if e != nil {
		t.Fatal(e)
	}
	c, srv := setup(t, a)
	if e = c.Store.SaveTarget(context.Background(), target); e != nil {
		t.Fatal(e)
	}
	m := manifest()
	m.TargetID = target.ID
	m.Name = "docker-" + domain.NewID()[:8]
	m.Components[0].Instances = 1
	m.Components[0].Port = 8080
	m.Components[0].HostPort = port
	m.Components[0].Image = image
	status, raw := request(t, srv, "POST", "/api/v1/applications", "", m)
	if status != 201 {
		t.Fatalf("create %d %s", status, raw)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	status, raw = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/deployments", "docker-first-release", nil)
	if status != 202 {
		t.Fatalf("deploy %d %s", status, raw)
	}
	var d domain.Deployment
	json.Unmarshal(raw, &d)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(context.Background())
	for ctx.Err() == nil {
		d, e = c.Store.Deployment(ctx, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(d.State) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if d.State != "succeeded" {
		t.Fatalf("Docker release failed %+v", d)
	}
	resource, e := a.Inspect(ctx, d.Steps[0].ResourceID)
	if e != nil {
		t.Fatal(e)
	}
	if !controller.Healthy(resource.Status) {
		t.Fatal(resource.Status)
	}
	response, e := http.Get(resource.URL + "/health/ready")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("readiness failed")
	}
	logs, e := a.Logs(ctx, resource.ID, 20)
	if e != nil {
		t.Fatal(e)
	}
	_ = logs
	all, e := a.Discover(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range all {
		if r.ID == resource.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("active instance not discovered")
	}
	// A repeat deployment reuses the active identity instead of spawning another instance.
	d2, e := c.Enqueue(ctx, app.ID, "docker-repeat-release")
	if e != nil {
		t.Fatal(e)
	}
	for ctx.Err() == nil {
		d2, e = c.Store.Deployment(ctx, d2.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(d2.State) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if d2.State != "succeeded" || d2.Steps[0].ResourceID != resource.ID {
		t.Fatalf("repeat changed instance identity %+v", d2)
	}
	if replacement := os.Getenv("OAP_TEST_DOCKER_REPLACEMENT_IMAGE"); replacement != "" {
		current, e := c.Store.Application(ctx, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		next := current.Manifest
		next.Components = append([]domain.Component(nil), next.Components...)
		next.Components[0].Image = replacement
		next.Components[0].Port = 8025
		updated, e := c.UpdateApplication(ctx, app.ID, next, current.Version)
		if e != nil {
			t.Fatal(e)
		}
		replacementDeployment, e := c.EnqueueVersion(ctx, app.ID, "docker-replacement-release", nil, updated.Version)
		if e != nil {
			t.Fatal(e)
		}
		for ctx.Err() == nil {
			replacementDeployment, e = c.Store.Deployment(ctx, replacementDeployment.ID)
			if e != nil {
				t.Fatal(e)
			}
			if domain.Terminal(replacementDeployment.State) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if replacementDeployment.State != "succeeded" || replacementDeployment.Steps[0].ResourceID != resource.ID || replacementDeployment.Steps[0].RemoteDeploymentID == d.Steps[0].RemoteDeploymentID {
			t.Fatalf("replacement failed %+v", replacementDeployment)
		}
		replacementResource, e := a.Inspect(ctx, resource.ID)
		if e != nil {
			t.Fatal(e)
		}
		if replacementResource.Image != replacement {
			t.Fatal("replacement image mismatch")
		}
		response, e := http.Get(replacementResource.URL)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("replacement endpoint unavailable")
		}
	}
	if e = a.Stop(context.Background(), resource.ID); e != nil {
		t.Fatal(e)
	}
	t.Logf("retained stopped Docker fixture reference=%s port=%d", resource.ID, port)
	if strings.Contains(resource.URL, "0.0.0.0") {
		t.Fatal("fixture exposed beyond loopback")
	}
}
