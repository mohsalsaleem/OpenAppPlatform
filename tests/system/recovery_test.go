package system_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func TestRecoveryRechecksWithoutRedispatchAndResumesPendingInstances(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 3
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, app.ID, "recover-release")
	if e != nil {
		t.Fatal(e)
	}
	// Two dispatches completed remotely, but one response was lost locally.
	for i := 0; i < 2; i++ {
		r, e := f.Ensure(ctx, operator.Spec{Name: domain.ResourceName(app.ID, "web", i+1), Component: m.Components[0]})
		if e != nil {
			t.Fatal(e)
		}
		remote, e := f.Deploy(ctx, r.ID)
		if e != nil {
			t.Fatal(e)
		}
		d.Steps[i].ResourceID = r.ID
		d.Steps[i].Phase = "attention"
		if i == 0 {
			d.Steps[i].RemoteDeploymentID = remote
		}
	}
	d.State = "attention"
	if e = c.Store.SaveDeployment(ctx, d); e != nil {
		t.Fatal(e)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	recoverPath := "/api/v1/deployments/" + d.ID + "/recover"
	req := controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: d.UpdatedAt}
	status, raw := request(t, srv, "POST", recoverPath, "", req)
	if status != 200 {
		t.Fatalf("recheck %d %s", status, raw)
	}
	var recovered domain.Deployment
	json.Unmarshal(raw, &recovered)
	if recovered.State != "attention" || recovered.Steps[0].Phase != "succeeded" {
		t.Fatalf("partial recovery %+v", recovered)
	}
	status, _ = request(t, srv, "POST", recoverPath, "", req)
	if status != 409 {
		t.Fatal("stale recovery accepted")
	}
	req = controller.Recovery{Component: "web", Ordinal: 2, ExpectedUpdatedAt: recovered.UpdatedAt}
	status, _ = request(t, srv, "POST", recoverPath, "", req)
	if status != 422 {
		t.Fatal("missing provider ID accepted")
	}
	req.RemoteDeploymentID = "remote-2"
	status, raw = request(t, srv, "POST", recoverPath, "", req)
	if status != 200 {
		t.Fatalf("attach %d %s", status, raw)
	}
	if f.deploys != 2 {
		t.Fatal("recovery dispatched another deployment")
	}
	c2, e := controller.New(ctx, c.Store, c.Factory)
	if e != nil {
		t.Fatal(e)
	}
	c2.PollInterval = 10 * time.Millisecond
	if e = c2.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c2.Jobs.Stop(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		recovered, e = c.Store.Deployment(ctx, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(recovered.State) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if recovered.State != "succeeded" {
		t.Fatalf("recovery did not finish %+v", recovered)
	}
	f.mu.Lock()
	count := f.deploys
	f.mu.Unlock()
	if count != 3 {
		t.Fatalf("expected only remaining instance dispatch; got %d", count)
	}
}

func TestLiveInstancesAndLogsUseBindingsInsteadOfLatestRelease(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	r := operator.Resource{ID: "runtime-instance", Status: "exited", Image: "nginx:1.27-alpine", ArtifactKind: "image"}
	f.resources[r.ID] = r
	if e = c.Store.Bind(ctx, "fixture", r.ID, app.ID, "web", 2); e != nil {
		t.Fatal(e)
	}
	status, raw := request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/instances", "", nil)
	if status != 200 {
		t.Fatalf("instances %d %s", status, raw)
	}
	var instances []controller.Instance
	json.Unmarshal(raw, &instances)
	if len(instances) != 2 || instances[0].Status != "not deployed" || instances[1].Status != "exited" {
		t.Fatalf("current states %+v", instances)
	}
	status, _ = request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/logs/web?ordinal=2", "", nil)
	if status != 200 {
		t.Fatal("bound logs require release history")
	}
	status, _ = request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/logs/web?ordinal=1", "", nil)
	if status != 404 {
		t.Fatal("logs leaked from a different instance")
	}
	status, _ = request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/logs/web?ordinal=0", "", nil)
	if status != 400 {
		t.Fatal("invalid ordinal accepted")
	}
	delete(f.resources, r.ID)
	status, raw = request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/instances", "", nil)
	if status != 200 {
		t.Fatal(status)
	}
	json.Unmarshal(raw, &instances)
	if instances[1].Status != "unavailable" {
		t.Fatal("missing resource reported healthy")
	}
}

func TestRecoveryAllowsPreparationRetryButNotUncertainRedispatch(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, app.ID, "prepare-recovery")
	if e != nil {
		t.Fatal(e)
	}
	d.State = "attention"
	d.Steps[0].Phase = "attention"
	d.Steps[0].RecoveryPhase = "dispatching"
	if e = c.Store.SaveDeployment(ctx, d); e != nil {
		t.Fatal(e)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	req := controller.Recovery{Component: "web", Ordinal: 1, RetryPreparation: true, ExpectedUpdatedAt: d.UpdatedAt}
	status, _ := request(t, srv, "POST", "/api/v1/deployments/"+d.ID+"/recover", "", req)
	if status != 422 {
		t.Fatal("uncertain dispatch accepted as preparation")
	}
	d.Steps[0].RecoveryPhase = "pending"
	if e = c.Store.SaveDeployment(ctx, d); e != nil {
		t.Fatal(e)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	req.ExpectedUpdatedAt = d.UpdatedAt
	status, raw := request(t, srv, "POST", "/api/v1/deployments/"+d.ID+"/recover", "", req)
	if status != 200 {
		t.Fatalf("preparation retry %d %s", status, raw)
	}
	var got domain.Deployment
	json.Unmarshal(raw, &got)
	if got.State != "running" || got.Steps[0].Phase != "pending" || f.deploys != 0 {
		t.Fatalf("preparation recovery %+v", got)
	}
}

type observationAdapter struct {
	*fake
	status operator.DeploymentStatus
}

func (a *observationAdapter) Observe(context.Context, string, string) (operator.DeploymentStatus, error) {
	return a.status, nil
}
func TestRecoveryRefreshesObservationDeadlineAndRejectsUnhealthyCompletion(t *testing.T) {
	adapter := &observationAdapter{fake: &fake{resources: map[string]operator.Resource{}}, status: operator.DeploymentStatus{State: "succeeded", ResourceStatus: "running:unhealthy"}}
	c, srv := setup(t, adapter)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, app.ID, "deadline-recovery")
	if e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-time.Hour)
	d.State = "attention"
	d.Steps[0].Phase = "attention"
	d.Steps[0].RemoteDeploymentID = "remote-1"
	d.Steps[0].ObservationStartedAt = &old
	if e = c.Store.SaveDeployment(ctx, d); e != nil {
		t.Fatal(e)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	req := controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: d.UpdatedAt}
	path := "/api/v1/deployments/" + d.ID + "/recover"
	status, _ := request(t, srv, "POST", path, "", req)
	if status != 422 {
		t.Fatal("unhealthy completion recovered")
	}
	req.RemoteDeploymentID = "different-operation"
	status, _ = request(t, srv, "POST", path, "", req)
	if status != 422 {
		t.Fatal("recorded operation replaced")
	}
	req.RemoteDeploymentID = ""
	adapter.status = operator.DeploymentStatus{State: "running", ResourceStatus: "starting"}
	status, raw := request(t, srv, "POST", path, "", req)
	if status != 200 {
		t.Fatalf("recovery %d %s", status, raw)
	}
	json.Unmarshal(raw, &d)
	if d.Steps[0].ObservationStartedAt == nil || time.Since(*d.Steps[0].ObservationStartedAt) > time.Minute {
		t.Fatal("observation deadline not refreshed")
	}
	c.Advance(ctx, d.ID)
	d, _ = c.Store.Deployment(ctx, d.ID)
	if d.State != "running" || d.Steps[0].Phase != "observing" {
		t.Fatal("recovery immediately timed out")
	}
}
