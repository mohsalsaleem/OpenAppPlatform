package system_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
)

func fixtureJSON(t *testing.T, url string) map[string]string {
	t.Helper()
	response, e := (&http.Client{}).Get(url)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("fixture HTTP %d", response.StatusCode)
	}
	var data map[string]string
	if e = json.NewDecoder(response.Body).Decode(&data); e != nil {
		t.Fatal(e)
	}
	return data
}
func TestDockerComponentConnectivityConfigurationScalingAndRestart(t *testing.T) {
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated Docker fixtures required")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	target := domain.Target{ID: "connected-docker", Name: "Connected Docker", Operator: "docker", URL: "unix://" + socket, Environment: "staging", Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, e := docker.New(target)
	if e != nil {
		t.Fatal(e)
	}
	c, _ := setup(t, adapter)
	ctx := context.Background()
	if e = c.Store.SaveTarget(ctx, target); e != nil {
		t.Fatal(e)
	}
	m := domain.Manifest{Name: "connected-" + domain.NewID()[:8], TargetID: target.ID, Environment: "staging", Components: []domain.Component{
		{Name: "api", Kind: "web", Image: image, Port: 8080, Instances: 1, Strategy: "standard", Env: map[string]string{"OAP_TEST_MESSAGE": "config-v1"}},
		{Name: "web", Kind: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, Strategy: "standard", Services: map[string]string{"UPSTREAM_URL": "api"}},
	}}
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := c.Enqueue(ctx, app.ID, "connected-initial")
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(ctx)
	initial = waitRelease(t, c, initial.ID)
	webRef := domain.ResourceName(app.ID, "web", 1)
	web, e := adapter.Inspect(ctx, webRef)
	if e != nil {
		t.Fatal(e)
	}
	defer adapter.Stop(ctx, webRef)
	defer adapter.Stop(ctx, domain.ResourceName(app.ID, "api", 1))
	defer adapter.Stop(ctx, domain.ResourceName(app.ID, "api", 2))
	upstream := fixtureJSON(t, web.URL+"/upstream")
	if upstream["message"] != "config-v1" {
		t.Fatal("DNS connection did not reach configured API", upstream)
	}
	before := fixtureJSON(t, web.URL)
	current, e := c.Store.Application(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	// Save a future value, then prove restart does not apply it.
	current.Manifest.Components[1].Env = map[string]string{"OAP_TEST_MESSAGE": "future-web-config"}
	current, e = c.UpdateApplication(ctx, app.ID, current.Manifest, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	restart, e := c.EnqueueRestart(ctx, app.ID, "connected-restart", controller.RestartRequest{Component: "web", Ordinal: 1}, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	restart = waitRelease(t, c, restart.ID)
	after := fixtureJSON(t, web.URL)
	if before["boot"] == after["boot"] || after["message"] != "" {
		t.Fatal("restart did not preserve current configuration", before, after)
	}
	if restart.Steps[0].RemoteDeploymentID != initial.Steps[1].RemoteDeploymentID {
		t.Fatal("restart replaced the container")
	}
	current.Manifest.Components[0].Instances = 2
	current, e = c.UpdateApplication(ctx, app.ID, current.Manifest, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	scaled, e := c.EnqueueVersion(ctx, app.ID, "connected-scale-up", nil, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	scaled = waitRelease(t, c, scaled.ID)
	if len(scaled.Steps) != 3 {
		t.Fatal("scale-up did not create both API instances")
	}
	if scaled.Steps[0].RemoteDeploymentID != initial.Steps[0].RemoteDeploymentID {
		t.Fatal("unchanged API was replaced during scale-up")
	}
	instances, e := c.Instances(ctx, app.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(instances) != 3 {
		t.Fatal(instances)
	}
	for _, instance := range instances {
		if !controller.Healthy(instance.Status) {
			t.Fatal(instance)
		}
	}
	fixtureJSON(t, web.URL+"/upstream")
	replacement := os.Getenv("OAP_TEST_DOCKER_REPLACEMENT_IMAGE")
	if replacement == "" {
		t.Fatal("replacement fixture required")
	}
	current.Manifest.Components[0].Port = 8025
	current.Manifest.Components[0].Image = replacement
	current.Manifest.Components[0].Env = map[string]string{"OAP_TEST_MESSAGE": "config-v2"}
	current, e = c.UpdateApplication(ctx, app.ID, current.Manifest, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	changed, e := c.EnqueueVersion(ctx, app.ID, "connected-config-update", nil, current.Version)
	if e != nil {
		t.Fatal(e)
	}
	waitRelease(t, c, changed.ID)
	upstream = fixtureJSON(t, web.URL+"/upstream")
	if upstream["version"] != "v2" || upstream["message"] != "config-v2" {
		t.Fatal("connection did not follow changed API port/configuration", upstream)
	}
	if fixtureJSON(t, web.URL)["message"] != "future-web-config" {
		t.Fatal("deployment did not apply saved configuration")
	}
	stored, e := c.Store.Deployment(ctx, initial.ID)
	if e != nil {
		t.Fatal(e)
	}
	if stored.Manifest.Components[0].Port != 8080 || stored.Manifest.Components[0].Env["OAP_TEST_MESSAGE"] != "config-v1" {
		t.Fatal("earlier release configuration changed")
	}
}
