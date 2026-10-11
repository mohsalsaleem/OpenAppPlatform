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
	for _, endpoint := range []string{base + "/route-reviews", base + "/route-publications/unknown", base + "/endpoint-verifications"} {
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
	c.EndpointProbe = func(context.Context, routing.Binding) routing.Verification {
		return routing.Verification{Status: "verified", CheckedAt: time.Now().UTC(), DNS: "matched", Addresses: []string{"192.0.2.1"}, TLS: "valid", Health: "ready", HTTPStatus: 200}
	}
	if code, raw = call("POST", base+"/endpoint-verifications", session, false, map[string]any{"name": b.Name, "expectedVersion": app.Version}); code != 200 {
		t.Fatal("owner endpoint verification failed", code, string(raw))
	}
	var probeAudit int
	if e = c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_audit WHERE user_id=$1 AND status=200 AND action LIKE '%endpoint-verifications'", owner).Scan(&probeAudit); e != nil || probeAudit != 1 {
		t.Fatal("verification audit missing", probeAudit, e)
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

func TestEndpointVerificationPersistsAndInvalidatesChangedAuthority(t *testing.T) {
	c, _, app, ctx, b, path := routeFixture(t)
	// Verification requires current native membership, independently of TLS success.
	calls := 0
	c.EndpointProbe = func(context.Context, routing.Binding) routing.Verification {
		calls++
		expires := time.Now().Add(time.Hour)
		return routing.Verification{Status: "verified", CheckedAt: time.Now().UTC(), DNS: "matched", Addresses: []string{"192.0.2.1"}, TLS: "valid", Health: "ready", HTTPStatus: 200, CertificateExpiresAt: &expires}
	}
	if _, e := c.VerifyEndpoint(ctx, app.ID, b.Name, app.Version); e == nil || calls != 0 {
		t.Fatal("unsynchronized route was probed", e)
	}
	plan, e := c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.ApplyRoute(ctx, app.ID, plan.ID, false); e != nil {
		t.Fatal(e)
	}
	report, e := c.VerifyEndpoint(ctx, app.ID, b.Name, app.Version)
	if e != nil || report.Status != "verified" || calls != 1 {
		t.Fatal(report, e)
	}
	fresh := &controller.Controller{Store: c.Store, Factory: c.Factory, RouteBindings: c.RouteBindings}
	view, e := fresh.Routes(ctx, app.ID)
	if e != nil || view.Endpoints[0].Verification == nil || view.Endpoints[0].Verification.Status != "verified" {
		t.Fatal("verification lost after controller recreation", view, e)
	}
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, append(raw, '\n'), 0644)
	view, e = fresh.Routes(ctx, app.ID)
	if e != nil || view.Endpoints[0].Verification.Status != "stale" {
		t.Fatal("native drift retained verified evidence", view, e)
	}
	if _, e = c.VerifyEndpoint(ctx, app.ID, b.Name, app.Version+1); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale application version probed", e)
	}
	agent := domain.WithPrincipal(context.Background(), domain.Principal{UserID: "agent", WorkspaceID: "default", Role: "owner", Kind: "agent", Scope: "operate"})
	if _, e = c.VerifyEndpoint(agent, app.ID, b.Name, app.Version); e == nil {
		t.Fatal("agent triggered trusted network probe")
	}
	if calls != 1 {
		t.Fatal("blocked calls reached the network")
	}
}

func TestRouteHealthExclusionRecoveryAndStaleReview(t *testing.T) {
	c, f, app, ctx, b, path := routeFixture(t)
	setStatus := func(id, status string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		r := f.resources[id]
		r.Status = status
		f.resources[id] = r
	}
	plan, err := c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	setStatus("backend-two", "running:unhealthy")
	before, _ := os.ReadFile(path)
	if _, err = c.ApplyRoute(ctx, app.ID, plan.ID, false); err == nil {
		t.Fatal("health changed after review but apply succeeded")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("stale health review changed native route")
	}
	view, err := c.Routes(ctx, app.ID)
	if err != nil || len(view.Endpoints[0].Excluded) != 1 || len(view.Endpoints[0].Desired) != 1 {
		t.Fatal(view, err)
	}
	plan, err = c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if err != nil || !plan.Members[1].Excluded || plan.Members[1].Status != "running:unhealthy" {
		t.Fatal(plan, err)
	}
	if _, err = c.ApplyRoute(ctx, app.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	// Recovery is reviewed explicitly; it does not rewrite routing in the background.
	setStatus("backend-two", "running:healthy")
	plan, err = c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if err != nil || len(plan.Added) != 1 {
		t.Fatal(plan, err)
	}
	if _, err = c.ApplyRoute(ctx, app.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	setStatus("backend-one", "stopped")
	plan, err = c.ReviewRoute(ctx, app.ID, b.Name, app.Version)
	if err != nil || len(plan.Removed) != 1 || plan.Removed[0] != f.resources["backend-one"].URL {
		t.Fatal(plan, err)
	}
	if _, err = c.ApplyRoute(ctx, app.ID, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(path)
	setStatus("backend-two", "unknown")
	if _, err = c.ReviewRoute(ctx, app.ID, b.Name, app.Version); err == nil {
		t.Fatal("empty healthy set accepted")
	}
	after, _ = os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("all-unhealthy observation changed native route")
	}
	// An excluded backend still requires verified ownership.
	setStatus("backend-two", "running:healthy")
	f.mu.Lock()
	r := f.resources["backend-one"]
	r.Description = "foreign"
	f.resources[r.ID] = r
	f.mu.Unlock()
	if _, err = c.ReviewRoute(ctx, app.ID, b.Name, app.Version); err == nil {
		t.Fatal("unhealthy foreign backend ownership bypassed")
	}
}
