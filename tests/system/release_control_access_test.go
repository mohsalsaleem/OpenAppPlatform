package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReleaseControlsRejectReadAgentsAndOwnerAgentsForAbandonment(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "control-access")
	if err != nil {
		t.Fatal(err)
	}
	service := access.Service{Pool: c.Store.Pool}
	owner, err := service.Register(ctx, "controls@example.invalid", "Owner", "control-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.Issue(ctx, owner, "agent", "Read", "read", app.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	operate, err := service.Issue(ctx, owner, "agent", "Operate", "operate", app.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer srv.Close()
	call := func(path, token string, body any) int {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	path := "/api/v1/deployments/" + d.ID
	req := controller.StopReleaseRequest{Mode: "cancel", Reason: "Access test", ExpectedUpdatedAt: d.UpdatedAt, AcknowledgePreparedChanges: true, AcknowledgeProviderMayContinue: true}
	if code := call(path+"/control", read, req); code != 403 {
		t.Fatal("read agent control", code)
	}
	req.Mode = "abandon"
	if code := call(path+"/control", operate, req); code != 403 {
		t.Fatal("owner agent abandoned", code)
	}
	if code := call(path+"/reconcile-abandoned", operate, controller.ReconcileReleaseRequest{ExpectedUpdatedAt: d.UpdatedAt, AcknowledgeCurrentRuntime: true}); code != 403 {
		t.Fatal("agent reconciled", code)
	}
	req.Mode = "cancel"
	if code := call(path+"/control", operate, req); code != 200 {
		t.Fatal("operate agent cancellation", code)
	}
	// Human owner sessions can close and reconcile queued work, without dispatch.
	next, err := c.Enqueue(ctx, app.ID, "owner-close-work")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Issue(ctx, owner, "session", "Owner session", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ownerCall := func(path string, body any) int {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(raw))
		req.AddCookie(&http.Cookie{Name: "oap-session", Value: session})
		req.Header.Set("X-OAP-CSRF", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	req.Mode = "abandon"
	req.ExpectedUpdatedAt = next.UpdatedAt
	if code := ownerCall("/api/v1/deployments/"+next.ID+"/control", req); code != 200 {
		t.Fatal("owner session abandonment", code)
	}
	next, err = c.Store.Deployment(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if code := ownerCall("/api/v1/deployments/"+next.ID+"/reconcile-abandoned", controller.ReconcileReleaseRequest{ExpectedUpdatedAt: next.UpdatedAt, AcknowledgeCurrentRuntime: true}); code != 200 {
		t.Fatal("owner session reconciliation", code)
	}

}
