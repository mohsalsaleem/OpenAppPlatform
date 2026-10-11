package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/routing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
)

func routeFixture(t *testing.T) (*controller.Controller, *fake, domain.Application, context.Context, routing.Binding, string) {
	t.Helper()
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	m := manifest()
	m.Name = "route-fixture"
	m.Components = m.Components[:1]
	m.Components[0].Instances = 2
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	app, e := c.Store.CreateApplication(context.Background(), m)
	if e != nil {
		t.Fatal(e)
	}
	b := routing.Binding{ApplicationID: app.ID, Component: m.Components[0].Name, TargetID: "fixture", Name: "oap-fixture", Domain: "shared.example.test", HealthPath: "/health/ready", Directory: t.TempDir()}
	c.RouteBindings = []routing.Binding{b}
	for i, id := range []string{"backend-one", "backend-two"} {
		f.resources[id] = operator.Resource{ID: id, Description: "OpenAppPlatform:" + app.ID + ":" + b.Component, ArtifactKind: "image", Image: m.Components[0].Image, Status: "running:healthy", URL: "https://" + id + ".example.test"}
		if e = c.Store.Bind(context.Background(), "fixture", id, app.ID, b.Component, i+1); e != nil {
			t.Fatal(e)
		}
	}
	raw, _ := routing.Render(b, []string{f.resources["backend-one"].URL})
	path := filepath.Join(b.Directory, b.Name+".yaml")
	if e = os.WriteFile(path, raw, 0644); e != nil {
		t.Fatal(e)
	}
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{UserID: "owner-test", WorkspaceID: "default", Role: "owner", Kind: "session", Scope: "operate"})
	return c, f, app, ctx, b, path
}
func TestReviewedRoutePublicationPersistsAndRejectsStaleAuthority(t *testing.T) {
	c, f, app, ctx, b, path := routeFixture(t)
	plan, e := c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if e != nil || len(plan.Added) != 1 || plan.Added[0] != f.resources["backend-two"].URL {
		t.Fatal(plan, e)
	}
	// Native editor drift must survive an attempted apply, without arming a fence.
	before, _ := os.ReadFile(path)
	os.WriteFile(path, append(before, '\n'), 0644)
	if _, e = c.ApplyRoute(ctx, app.ID, plan.ID, false); e == nil {
		t.Fatal("native drift accepted")
	}
	unresolved, _ := db.New(c.Store.Pool).HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: app.ID})
	if unresolved {
		t.Fatal("preflight failure armed publication fence")
	}
	os.WriteFile(path, before, 0644)
	target, _ := c.Store.Target(ctx, "fixture")
	target.TokenEnv = "ROTATED_AUTHORITY"
	c.Store.SaveTarget(ctx, target)
	if _, e = c.ApplyRoute(ctx, app.ID, plan.ID, false); e == nil {
		t.Fatal("credential authority drift accepted")
	}
	plan, e = c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	original := plan.DesiredConfig
	plan.DesiredConfig = `{"http":{"routers":{"foreign":{}}}}`
	tampered, _ := json.Marshal(plan)
	if _, e = c.Store.Pool.Exec(ctx, "UPDATE oap_route_publications SET plan=$2 WHERE id=$1", plan.ID, tampered); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ApplyRoute(ctx, app.ID, plan.ID, false); e == nil {
		t.Fatal("altered stored native bytes were applied")
	}
	plan.DesiredConfig = original
	restored, _ := json.Marshal(plan)
	c.Store.Pool.Exec(ctx, "UPDATE oap_route_publications SET plan=$2 WHERE id=$1", plan.ID, restored)
	applied, e := c.ApplyRoute(ctx, app.ID, plan.ID, false)
	if e != nil || applied.State != "applied" {
		t.Fatal(applied, e)
	}
	stat, _ := os.Stat(path)
	retry, e := c.ApplyRoute(ctx, app.ID, plan.ID, false)
	after, _ := os.Stat(path)
	if e != nil || retry.State != "applied" || !os.SameFile(stat, after) {
		t.Fatal("retry replaced route", e)
	}
	fresh := &controller.Controller{Store: c.Store, Factory: c.Factory, RouteBindings: c.RouteBindings}
	view, e := fresh.Routes(ctx, app.ID)
	if e != nil || view.Endpoints[0].Status != "in-sync" || view.Publications[0].ID != plan.ID {
		t.Fatal("state lost across controller recreation", view, e)
	}
	for _, p := range []domain.Principal{{UserID: "agent", WorkspaceID: "default", Role: "owner", Kind: "agent", Scope: "operate"}, {UserID: "operator", WorkspaceID: "default", Role: "operator", Kind: "session", Scope: "operate"}} {
		if _, e = c.ReviewRoute(domain.WithPrincipal(context.Background(), p), app.ID, b.Name, app.Version); e == nil {
			t.Fatal("non-owner publication allowed")
		}
	}
	f.mu.Lock()
	r := f.resources["backend-two"]
	r.Description = "foreign"
	f.resources[r.ID] = r
	f.mu.Unlock()
	if _, e = c.ReviewRoute(ctx, app.ID, b.Name, app.Version); e == nil {
		t.Fatal("foreign ownership accepted")
	}
}
func TestInterruptedRoutePublicationFencesLifecycleAndResumesExactIntent(t *testing.T) {
	c, _, app, ctx, b, path := routeFixture(t)
	plan, e := c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 21; n++ {
		if _, e = c.ReviewRoute(ctx, app.ID, b.Name, app.Version); e != nil {
			t.Fatal(e)
		}
	}
	queries := db.New(c.Store.Pool)
	if e = queries.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app.ID, ID: plan.ID, State: "publishing"}); e != nil {
		t.Fatal(e)
	}
	view, e := c.Routes(ctx, app.ID)
	if e != nil || len(view.Publications) != 20 || view.Publications[0].ID != plan.ID || view.Publications[0].State != "publishing" {
		t.Fatal("old unresolved intent was hidden by recent reviews", view, e)
	}
	if _, e = c.EnqueueVersion(ctx, app.ID, "route-fenced-deployment", nil, app.Version); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("uncertain publication did not fence deployment", e)
	}
	next := app.Manifest
	next.Components[0].Instances = 3
	if _, e = c.UpdateApplication(ctx, app.ID, next, app.Version); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("uncertain publication did not fence configuration", e)
	}
	if _, e = c.ReviewRoute(ctx, app.ID, b.Name, app.Version); e == nil {
		t.Fatal("new review bypassed recovery")
	}
	// Simulate the native write succeeding immediately before controller death.
	os.WriteFile(path, []byte(plan.DesiredConfig), 0644)
	stat, _ := os.Stat(path)
	if _, e = c.ApplyRoute(ctx, app.ID, plan.ID, true); e == nil {
		t.Fatal("changed native outcome was cancelled")
	}
	fresh := &controller.Controller{Store: c.Store, Factory: c.Factory, RouteBindings: c.RouteBindings}
	result, e := fresh.ApplyRoute(ctx, app.ID, plan.ID, false)
	after, _ := os.Stat(path)
	if e != nil || result.State != "applied" || !os.SameFile(stat, after) {
		t.Fatal("exact publication did not recover without replacing file", e)
	}
	if _, e = c.UpdateApplication(ctx, app.ID, next, app.Version); e != nil {
		t.Fatal("resolved publication still fenced", e)
	}
	if _, e = c.ReviewRoute(ctx, app.ID, b.Name, app.Version); e == nil {
		t.Fatal("stale version accepted")
	}
	// Database fence also covers direct admission/configuration paths.
	_, e = c.Store.Pool.Exec(ctx, "UPDATE oap_route_publications SET state='publishing' WHERE id=$1", plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Store.Pool.Exec(ctx, "UPDATE oap_applications SET spec=spec WHERE id=$1", app.ID)
	if e == nil {
		t.Fatal("database definition fence bypassed")
	}
}
func TestRouteCancellationRequiresUnchangedNativeOutcome(t *testing.T) {
	c, _, app, ctx, b, _ := routeFixture(t)
	plan, e := c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	db.New(c.Store.Pool).SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app.ID, ID: plan.ID, State: "publishing"})
	result, e := c.ApplyRoute(ctx, app.ID, plan.ID, true)
	if e != nil || result.State != "cancelled" {
		t.Fatal(result, e)
	}
	if _, e = c.ApplyRoute(ctx, app.ID, plan.ID, false); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("cancelled intent applied", e)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), b.Directory) || strings.Contains(string(raw), "tokenEnv") {
		t.Fatal("private route authority leaked")
	}
}

func TestRouteHTTPRequiresOwnerSessionAndRecordsMutationAudit(t *testing.T) {
	c, _, app, _, b, path := routeFixture(t)
	ctx := context.Background()
	service := access.Service{Pool: c.Store.Pool}
	owner, e := service.Register(ctx, "route-owner@example.invalid", "Owner", "route-owner-password", "")
	if e != nil {
		t.Fatal(e)
	}
	session, e := service.Issue(ctx, owner, "session", "Owner", "operate", "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	agent, e := service.Issue(ctx, owner, "agent", "Agent", "operate", app.ID, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer srv.Close()
	call := func(method, path, credential string, bearer bool, body any) (int, []byte) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(raw))
		if bearer {
			req.Header.Set("Authorization", "Bearer "+credential)
		} else {
			req.AddCookie(&http.Cookie{Name: "oap-session", Value: credential})
			req.Header.Set("X-OAP-CSRF", "1")
		}
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		output, _ := io.ReadAll(response.Body)
		return response.StatusCode, output
	}
	base := "/api/v1/applications/" + app.ID
	before, _ := os.ReadFile(path)
	for _, endpoint := range []string{base + "/route-reviews", base + "/route-publications/unknown"} {
		if code, _ := call("POST", endpoint, agent, true, map[string]any{"name": b.Name, "expectedVersion": app.Version}); code != 403 {
			t.Fatal("agent native publication was not denied", code)
		}
	}
	if code, _ := call("GET", base+"/routes", agent, true, nil); code != 200 {
		t.Fatal("scoped route inspection failed", code)
	}
	code, raw := call("POST", base+"/route-reviews", session, false, map[string]any{"name": b.Name, "expectedVersion": app.Version})
	if code != 201 {
		t.Fatal("owner review failed", code, string(raw))
	}
	var plan controller.RoutePlan
	json.Unmarshal(raw, &plan)
	if code, raw = call("POST", base+"/route-publications/"+plan.ID, session, false, map[string]string{"action": "apply"}); code != 200 {
		t.Fatal("owner apply failed", code, string(raw))
	}
	after, _ := os.ReadFile(path)
	if routing.Hash(before) == routing.Hash(after) {
		t.Fatal("owner apply did not update native membership")
	}
	var audit int
	if e = c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_audit WHERE user_id=$1 AND status IN (200,201) AND action LIKE '%route-%'", owner).Scan(&audit); e != nil || audit != 2 {
		t.Fatal("routing audit was not completed", audit, e)
	}
}
