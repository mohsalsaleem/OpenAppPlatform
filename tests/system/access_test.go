package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnerSessionsRolesScopeAuditAndRevokedJobs(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	c.RequireIdentity = true
	srv := httptest.NewServer((&httpapi.Server{Controller: c, SetupToken: strings.Repeat("s", 32), Token: platformToken}).Handler())
	defer srv.Close()
	call := func(method, path string, body any, cookie *http.Cookie, bearer, workspace string) (int, []byte, []*http.Cookie) {
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		r.Header.Set("X-OAP-CSRF", "1")
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if bearer != "" {
			r.Header.Set("Authorization", bearer)
		}
		if workspace != "" {
			r.Header.Set("X-OAP-Workspace", workspace)
		}
		r.Header.Set("Idempotency-Key", "auth-test-release")
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, raw, res.Cookies()
	}
	account := map[string]string{"email": "owner@example.invalid", "name": "Owner", "password": "owner-password-long"}
	code, _, _ := call("POST", "/api/v1/auth/setup", account, nil, "Setup wrong", "")
	if code != 401 {
		t.Fatal("bootstrap accepted without secret")
	}
	code, raw, cookies := call("POST", "/api/v1/auth/setup", account, nil, "Setup "+strings.Repeat("s", 32), "")
	if code != 200 || len(cookies) != 1 {
		t.Fatalf("setup %d %s", code, raw)
	}
	owner := cookies[0]
	if !owner.HttpOnly || owner.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	code, _, _ = call("POST", "/api/v1/auth/setup", account, nil, "Setup "+strings.Repeat("s", 32), "")
	if code != 409 {
		t.Fatal("second owner allowed")
	}
	code, _, _ = call("GET", "/api/v1/applications", nil, nil, "Bearer "+platformToken, "")
	if code != 401 {
		t.Fatal("preview token bypassed owner mode")
	}
	code, _, _ = call("GET", "/api/v1/applications", nil, owner, "", "other")
	if code != 404 {
		t.Fatal("other workspace accepted")
	}
	m := manifest()
	m.Name = "owned-app"
	code, raw, _ = call("POST", "/api/v1/applications", m, owner, "", "")
	if code != 201 {
		t.Fatalf("create %d %s", code, raw)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	code, raw, _ = call("POST", "/api/v1/access/invitations", map[string]string{"email": "viewer@example.invalid", "role": "viewer"}, owner, "", "")
	if code != 201 {
		t.Fatalf("invite %d %s", code, raw)
	}
	var invitation map[string]string
	json.Unmarshal(raw, &invitation)
	code, raw, cookies = call("POST", "/api/v1/auth/invitations/accept", map[string]string{"email": "viewer@example.invalid", "name": "Viewer", "password": "viewer-password-long", "invite": invitation["invite"]}, nil, "", "")
	if code != 200 {
		t.Fatalf("join %d %s", code, raw)
	}
	viewer := cookies[0]
	var member domain.Principal
	json.Unmarshal(raw, &member)
	code, _, _ = call("POST", "/api/v1/applications", m, viewer, "", "")
	if code != 403 {
		t.Fatal("viewer write allowed")
	}
	code, _, _ = call("GET", "/api/v1/access/members", nil, viewer, "", "")
	if code != 403 {
		t.Fatal("viewer owner access allowed")
	}
	code, _, _ = call("GET", "/api/v1/applications/"+app.ID, nil, viewer, "", "")
	if code != 200 {
		t.Fatal("viewer read blocked")
	}
	issue := func(scope string) string {
		code, raw, _ := call("POST", "/api/v1/access/credentials", map[string]string{"name": "test-agent", "scope": scope, "applicationId": app.ID}, owner, "", "")
		if code != 201 {
			t.Fatalf("issue %d %s", code, raw)
		}
		var v map[string]string
		json.Unmarshal(raw, &v)
		return v["token"]
	}
	read := issue("read")
	code, _, _ = call("GET", "/api/v1/applications/"+app.ID, nil, nil, "Bearer "+read, "")
	if code != 200 {
		t.Fatal("scoped read denied")
	}
	for _, path := range []string{"/api/v1/targets", "/api/v1/applications/foreign", "/api/v1/access/members"} {
		code, _, _ = call("GET", path, nil, nil, "Bearer "+read, "")
		if code == 200 {
			t.Fatal("scope bypass", path)
		}
	}
	code, _, _ = call("POST", "/api/v1/applications/"+app.ID+"/deployments", nil, nil, "Bearer "+read, "")
	if code != 403 {
		t.Fatal("read token dispatched")
	}
	operate := issue("operate")
	code, raw, _ = call("POST", "/api/v1/applications/"+app.ID+"/deployments", nil, nil, "Bearer "+operate, "")
	if code != 202 {
		t.Fatalf("enqueue %d %s", code, raw)
	}
	var d domain.Deployment
	json.Unmarshal(raw, &d)
	principal, e := (access.Service{Pool: c.Store.Pool}).Principal(context.Background(), operate)
	if e != nil {
		t.Fatal(e)
	}
	code, _, _ = call("DELETE", "/api/v1/access/credentials/"+principal.CredentialID, nil, owner, "", "")
	if code != 200 {
		t.Fatal("revoke failed")
	}
	if e = c.Advance(context.Background(), d.ID); e != nil {
		t.Fatal(e)
	}
	d, e = c.Store.Deployment(context.Background(), d.ID)
	if e != nil || d.State != "attention" || f.deploys != 0 || len(f.resources) != 0 {
		t.Fatal("revoked queued action mutated provider")
	}
	code, _, _ = call("GET", "/api/v1/applications/"+app.ID, nil, nil, "Bearer "+operate, "")
	if code != 401 {
		t.Fatal("revoked credential accepted")
	}
	code, _, _ = call("PATCH", "/api/v1/access/members/"+member.UserID, map[string]any{"role": "viewer", "active": false}, owner, "", "")
	if code != 200 {
		t.Fatal("disable failed")
	}
	code, _, _ = call("GET", "/api/v1/applications", nil, viewer, "", "")
	if code != 401 {
		t.Fatal("disabled member session accepted")
	}
	code, raw, _ = call("GET", "/api/v1/access/audit", nil, owner, "", "")
	if code != 200 || !bytes.Contains(raw, []byte("POST /api/v1/applications")) || bytes.Contains(raw, []byte(operate)) || bytes.Contains(raw, []byte(account["password"])) {
		t.Fatal("audit missing or secret leaked")
	}
	// Session mutation requires the custom CSRF header even with a valid cookie.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/auth/logout", nil)
	req.AddCookie(owner)
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("CSRF-less mutation allowed")
	}
	code, _, _ = call("POST", "/api/v1/auth/logout", nil, owner, "", "")
	if code != 200 {
		t.Fatal("logout failed")
	}
	code, _, _ = call("GET", "/api/v1/meta", nil, owner, "", "")
	if code != 401 {
		t.Fatal("logged-out cookie accepted")
	}
}
