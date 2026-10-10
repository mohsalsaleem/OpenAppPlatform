package system_test

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"testing"
)

func TestReleaseCancellationPersistsWithoutDispatch(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "cancel-one")
	if err != nil {
		t.Fatal(err)
	}
	req := controller.StopReleaseRequest{Mode: "cancel", Reason: "Owner chose to defer", ExpectedUpdatedAt: d.UpdatedAt, AcknowledgePreparedChanges: true}
	status, raw := request(t, srv, "POST", "/api/v1/deployments/"+d.ID+"/control", "", req)
	if status != 200 {
		t.Fatalf("cancel: %d %s", status, raw)
	}
	closed, err := c.Store.Deployment(ctx, d.ID)
	if err != nil || closed.State != "cancelled" || closed.Control == nil {
		t.Fatalf("closure not durable: %+v %v", closed, err)
	}
	if _, err = c.StopRelease(ctx, d.ID, req); err != nil {
		t.Fatal("identical retry", err)
	}
	req.Reason = "changed"
	if _, err = c.StopRelease(ctx, d.ID, req); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting closure: %v", err)
	}
	c2, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if err = c2.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if f.deploys != 0 {
		t.Fatal("cancel dispatched provider work")
	}
	if _, err = c.Enqueue(ctx, app.ID, "after-cancel"); err != nil {
		t.Fatal("cancel left fence", err)
	}
}

func TestAbandonmentRetainsFenceUntilExactReconciliation(t *testing.T) {
	f := &controlFake{fake: &fake{resources: map[string]operator.Resource{}}, running: true}
	c, _ := setup(t, f)
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "abandon-one")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Ensure(ctx, operator.Spec{Name: domain.ResourceName(app.ID, "web", 1), Ownership: "OpenAppPlatform:" + app.ID + ":web", Component: d.Manifest.Components[0]})
	if err != nil {
		t.Fatal(err)
	}
	d.State = "attention"
	d.Steps[0].ResourceID = r.ID
	d.Steps[0].Phase = "attention"
	d.Steps[0].RecoveryPhase = "dispatching"
	if err = c.Store.SaveDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	stop := controller.StopReleaseRequest{Mode: "cancel", Reason: "Lost response", ExpectedUpdatedAt: d.UpdatedAt, AcknowledgePreparedChanges: true}
	if _, err = c.StopRelease(ctx, d.ID, stop); err == nil {
		t.Fatal("uncertain dispatch cancelled")
	}
	stop.Mode = "abandon"
	stop.AcknowledgeProviderMayContinue = true
	d, err = c.StopRelease(ctx, d.ID, stop)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Enqueue(ctx, app.ID, "fenced-release"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("abandon did not fence: %v", err)
	}
	if err = c.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	reconcile := controller.ReconcileReleaseRequest{ExpectedUpdatedAt: d.UpdatedAt, AcknowledgeCurrentRuntime: true}
	if _, err = c.ReconcileAbandoned(ctx, d.ID, reconcile); err == nil {
		t.Fatal("missing provider ID released fence")
	}
	reconcile.Operations = []domain.ProviderReconciliation{{Component: "web", Ordinal: 1, RemoteDeploymentID: "remote-terminal"}}
	if _, err = c.ReconcileAbandoned(ctx, d.ID, reconcile); err == nil {
		t.Fatal("running operation released fence")
	}
	f.running = false
	d, err = c.ReconcileAbandoned(ctx, d.ID, reconcile)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != "abandoned" || d.Control.ResolvedAt == nil {
		t.Fatalf("incorrect reconciliation %+v", d)
	}
	if _, err = c.Enqueue(ctx, app.ID, "after-reconcile"); err != nil {
		t.Fatal(err)
	}
	if f.deploys != 0 {
		t.Fatal("control dispatched provider work")
	}
}

// Provider state is independent of the local closure ledger.
type controlFake struct {
	*fake
	running bool
}

func (f *controlFake) Observe(ctx context.Context, remote, resource string) (operator.DeploymentStatus, error) {
	if f.running {
		return operator.DeploymentStatus{State: "running", ResourceStatus: "running"}, nil
	}
	return f.fake.Observe(ctx, remote, resource)
}

func TestReleaseControlConflictsWithWorkerAndRetainsPreparedResources(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "prepared-cancel")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Ensure(ctx, operator.Spec{Name: domain.ResourceName(app.ID, "web", 1), Ownership: "OpenAppPlatform:" + app.ID + ":web", Component: d.Manifest.Components[0]})
	if err != nil {
		t.Fatal(err)
	}
	d.Steps[0].Phase = "prepared"
	d.Steps[0].ResourceID = r.ID
	if err = c.Store.SaveDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	req := controller.StopReleaseRequest{Mode: "cancel", Reason: "Retain preparation", ExpectedUpdatedAt: d.UpdatedAt, AcknowledgePreparedChanges: true}
	conn, err := c.Store.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.StopRelease(ctx, d.ID, req); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("closed while worker held lock: %v", err)
	}
	conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtext($1))", d.ID)
	conn.Release()
	d, err = c.StopRelease(ctx, d.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.resources) != 1 || d.Steps[0].Phase != "prepared" || d.Steps[0].ResourceID != r.ID {
		t.Fatal("cancellation erased provider evidence")
	}
}
