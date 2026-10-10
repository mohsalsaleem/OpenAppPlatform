package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseComparisonIsSanitizedScopedAndOperatorIndependent(t *testing.T) {
	f := &restartFake{fake: &fake{resources: map[string]operator.Resource{}}}
	c, srv := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Env = map[string]string{"SETTING": "private-before"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	old, err := c.Enqueue(ctx, app.ID, "comparison-old")
	if err != nil {
		t.Fatal(err)
	}
	old.State = "succeeded"
	for i := range old.Steps {
		old.Steps[i].Phase = "succeeded"
	}
	if err = c.Store.SaveDeployment(ctx, old); err != nil {
		t.Fatal(err)
	}
	changed := app.Manifest
	changed.Components[0].Image = "registry.example/web@sha256:" + strings.Repeat("b", 64)
	changed.Components[0].Env = map[string]string{"SETTING": "private-after"}
	changed.Components[0].Readiness = &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}
	if _, err = c.UpdateApplication(ctx, app.ID, changed, app.Version); err != nil {
		t.Fatal(err)
	}
	newer, err := c.Enqueue(ctx, app.ID, "comparison-new")
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := c.Enqueue(ctx, other.ID, "comparison-foreign")
	if err != nil {
		t.Fatal(err)
	}
	var adapterCalls atomic.Int64
	c.Factory = func(domain.Target) (operator.Adapter, error) { adapterCalls.Add(1); return f, nil }
	path := "/api/v1/applications/" + app.ID + "/release-comparison?from=" + old.ID + "&to=" + newer.ID
	code, body := request(t, srv, "GET", path, "", nil)
	if code != 200 || strings.Contains(string(body), "private-before") || strings.Contains(string(body), "private-after") {
		t.Fatal("comparison failed or leaked variable values")
	}
	var result domain.ReleaseComparison
	if err = json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.From.ID != old.ID || result.To.ID != newer.ID || len(result.Changes) < 3 || !result.To.Artifacts[0].Immutable {
		t.Fatal("snapshot/artifact comparison incorrect")
	}
	code, _ = request(t, srv, "GET", strings.Replace(path, newer.ID, foreign.ID, 1), "", nil)
	if code != 404 {
		t.Fatal("cross-application release visible")
	}
	code, _ = request(t, srv, "GET", strings.Replace(path, newer.ID, old.ID, 1), "", nil)
	if code != 400 {
		t.Fatal("same release accepted")
	}
	service := access.Service{Pool: c.Store.Pool}
	owner, err := service.Register(ctx, "comparison-owner@example.invalid", "Owner", "comparison-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.Issue(ctx, owner, "agent", "Compare", "read", app.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	scoped := httptest.NewServer((&httpapi.Server{Controller: c, AuthMode: "owner"}).Handler())
	defer scoped.Close()
	call := func(path string) int {
		r, _ := http.NewRequest("GET", scoped.URL+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if call(path) != 200 || call(strings.Replace(path, "/applications/"+app.ID, "/applications/"+other.ID, 1)) != 403 || call(strings.Replace(path, newer.ID, foreign.ID, 1)) != 404 {
		t.Fatal("scoped comparison crossed application boundary")
	}
	if adapterCalls.Load() != 0 || f.deploys != 0 {
		t.Fatal("comparison contacted or mutated operator")
	}
	persisted, _ := c.Store.Deployment(ctx, newer.ID)
	if persisted.State != newer.State {
		t.Fatal("comparison changed release state")
	}
}
