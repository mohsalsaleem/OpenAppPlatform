package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/routing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
)

func automationFixture(t *testing.T) (*controller.Controller, *fake, domain.Application, context.Context, routing.Binding, string) {
	t.Helper()
	c, f, app, _, b, path := routeFixture(t)
	owner, e := (access.Service{Pool: c.Store.Pool}).Register(context.Background(), "automation-owner@example.invalid", "Owner", "automation-owner-password", "")
	if e != nil {
		t.Fatal(e)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{UserID: owner, WorkspaceID: "default", Role: "owner", Kind: "session", Scope: "operate"})
	raw, e := routing.Render(b, []string{f.resources["backend-one"].URL, f.resources["backend-two"].URL})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0644); e != nil {
		t.Fatal(e)
	}
	return c, f, app, ctx, b, path
}
func nativeStatus(f *fake, id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.resources[id]
	r.Status = status
	f.resources[id] = r
}
func TestAutomaticRouteMembershipOptInHealthRecoveryDriftAndDisable(t *testing.T) {
	c, f, app, ctx, b, path := automationFixture(t)
	before, _ := os.ReadFile(path)
	nativeStatus(f, "backend-two", "running:unhealthy")
	if e := c.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("default-off changed route")
	}
	if _, e := c.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e == nil {
		t.Fatal("outdated membership enabled")
	}
	nativeStatus(f, "backend-two", "running:healthy")
	p, _ := domain.Identity(ctx)
	p.Kind = "agent"
	if _, e := c.SetRouteAutomation(domain.WithPrincipal(context.Background(), p), app.ID, b.Name, app.Version, true); e == nil {
		t.Fatal("agent granted automation")
	}
	if _, e := c.SetRouteAutomation(ctx, app.ID, b.Name, app.Version+1, true); e == nil {
		t.Fatal("stale version granted automation")
	}
	if _, e := c.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ReviewRoute(ctx, app.ID, b.Name, app.Version); e == nil {
		t.Fatal("manual publisher bypassed automation ownership")
	}
	nativeStatus(f, "backend-two", "stopped")
	if e := c.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := c.Routes(ctx, app.ID)
	if e != nil || v.Endpoints[0].Status != "in-sync" || len(v.Endpoints[0].Current) != 1 || !v.Publications[0].Automatic {
		t.Fatal(v, e)
	}
	first := v.Publications[0].ID
	if e = c.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	v, e = c.Routes(ctx, app.ID)
	if e != nil || v.Publications[0].ID != first {
		t.Fatal("idle tick created another publication", e)
	}
	nativeStatus(f, "backend-one", "unknown")
	before, _ = os.ReadFile(path)
	if e = c.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("empty healthy set changed native route")
	}
	v, _ = c.Routes(ctx, app.ID)
	if !v.Endpoints[0].Automation.Enabled || v.Endpoints[0].Automation.Status != "blocked" {
		t.Fatal(v)
	}
	nativeStatus(f, "backend-one", "running:healthy")
	nativeStatus(f, "backend-two", "running:healthy")
	fresh := &controller.Controller{Store: c.Store, Factory: c.Factory, RouteBindings: c.RouteBindings}
	if e = fresh.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	v, e = fresh.Routes(ctx, app.ID)
	if e != nil || len(v.Endpoints[0].Current) != 2 {
		t.Fatal(v, e)
	}
	// Even a harmless native formatting edit must be preserved and pause the grant.
	raw, _ := os.ReadFile(path)
	edited := append(raw, '\n')
	os.WriteFile(path, edited, 0644)
	if e = fresh.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(edited, after) {
		t.Fatal("native edit overwritten")
	}
	v, _ = fresh.Routes(ctx, app.ID)
	if v.Endpoints[0].Automation.Enabled || v.Endpoints[0].Automation.Status != "paused" {
		t.Fatal(v)
	}
	if _, e = fresh.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, false); e != nil {
		t.Fatal(e)
	}
	nativeStatus(f, "backend-two", "stopped")
	if e = fresh.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(edited, after) {
		t.Fatal("disabled automation changed route")
	}
}
func TestAutomaticRoutePublicationCrashRecoveryAndOwnerRevocation(t *testing.T) {
	c, f, app, ctx, b, path := automationFixture(t)
	if _, e := c.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e != nil {
		t.Fatal(e)
	}
	nativeStatus(f, "backend-two", "stopped")
	if e := c.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	view, _ := c.Routes(ctx, app.ID)
	plan := view.Publications[0]
	q := db.New(c.Store.Pool)
	// Recreate the durable state immediately after native write and before DB acknowledgement.
	if e := q.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app.ID, ID: plan.ID, State: "publishing"}); e != nil {
		t.Fatal(e)
	}
	if e := q.UpdateRouteAutomation(ctx, db.UpdateRouteAutomationParams{ApplicationID: app.ID, Name: b.Name, Enabled: true, NativeHash: plan.BeforeHash, PendingID: plan.ID, Status: "updating"}); e != nil {
		t.Fatal(e)
	}
	if _, e := c.EnqueueVersion(ctx, app.ID, "automatic-fence", nil, app.Version); e == nil {
		t.Fatal("uncertain write did not fence lifecycle")
	}
	stat, _ := os.Stat(path)
	fresh := &controller.Controller{Store: c.Store, Factory: c.Factory, RouteBindings: c.RouteBindings}
	if e := fresh.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(stat, after) {
		t.Fatal("recovery replaced already desired file")
	}
	row, e := q.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app.ID, Name: b.Name})
	if e != nil || row.PendingID != "" || row.NativeHash != plan.DesiredHash {
		t.Fatal(row, e)
	}
	// A health change during uncertain intent requires human recovery and retains the fence.
	q.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app.ID, ID: plan.ID, State: "publishing"})
	q.UpdateRouteAutomation(ctx, db.UpdateRouteAutomationParams{ApplicationID: app.ID, Name: b.Name, Enabled: true, NativeHash: plan.BeforeHash, PendingID: plan.ID, Status: "updating"})
	nativeStatus(f, "backend-two", "running:healthy")
	if e = fresh.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	row, _ = q.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app.ID, Name: b.Name})
	if row.Enabled || row.Status != "paused" {
		t.Fatal(row)
	}
	uncertain, _ := q.HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: app.ID})
	if !uncertain {
		t.Fatal("uncertain fence cleared")
	}
	nativeStatus(f, "backend-two", "stopped")
	if _, e = fresh.ApplyRoute(ctx, app.ID, plan.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e != nil {
		t.Fatal(e)
	}
	principal, _ := domain.Identity(ctx)
	if _, e = c.Store.Pool.Exec(ctx, "UPDATE oap_users SET active=false WHERE id=$1", principal.UserID); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(path)
	nativeStatus(f, "backend-two", "running:healthy")
	if e = fresh.ReconcileRoutes(ctx); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Equal(before, raw) {
		t.Fatal("revoked owner grant changed native route")
	}
	row, _ = q.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app.ID, Name: b.Name})
	if row.Enabled {
		t.Fatal("revoked owner grant remains enabled")
	}
	payload, _ := json.Marshal(view)
	if bytes.Contains(payload, []byte(b.Directory)) {
		t.Fatal("private route directory leaked")
	}
}

func TestAutomaticRouteAuthorityChangesAndReplacedDirectoryPauseWithoutWrites(t *testing.T) {
	for _, mode := range []string{"binding", "target", "directory"} {
		t.Run(mode, func(t *testing.T) {
			c, f, app, ctx, b, path := automationFixture(t)
			if _, e := c.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(path)
			switch mode {
			case "binding":
				c.RouteBindings[0].ExpectedAddresses = []string{"192.0.2.10"}
			case "target":
				target, _ := c.Store.Target(ctx, "fixture")
				target.TokenEnv = "CHANGED_AUTHORITY"
				if e := c.Store.SaveTarget(ctx, target); e != nil {
					t.Fatal(e)
				}
			case "directory":
				if e := os.Rename(b.Directory, b.Directory+"-old"); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { os.RemoveAll(b.Directory + "-old") })
				if e := os.Mkdir(b.Directory, 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, before, 0644); e != nil {
					t.Fatal(e)
				}
			}
			nativeStatus(f, "backend-two", "stopped")
			if e := c.ReconcileRoutes(ctx); e != nil {
				t.Fatal(e)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("changed authority wrote route")
			}
			row, e := db.New(c.Store.Pool).GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app.ID, Name: b.Name})
			if e != nil || row.Enabled || row.Status != "paused" {
				t.Fatal(row, e)
			}
		})
	}
}

func TestAutomaticRouteRiverWorkerRunsPersistedGrant(t *testing.T) {
	c, f, app, ctx, b, _ := automationFixture(t)
	if _, e := c.SetRouteAutomation(ctx, app.ID, b.Name, app.Version, true); e != nil {
		t.Fatal(e)
	}
	nativeStatus(f, "backend-two", "stopped")
	if e := c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := c.Jobs.Stop(stop); e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, e := c.Routes(ctx, app.ID)
		if e == nil && len(v.Endpoints[0].Current) == 1 && v.Endpoints[0].Status == "in-sync" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("periodic River worker did not reconcile persisted owner grant")
}
