package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
)

func TestRestrictedAgentsAdmissionAndQueuedAuthority(t *testing.T) {
	ctx := context.Background()
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	c.RequireIdentity = true
	service := access.Service{Pool: c.Store.Pool}
	user, err := service.Register(ctx, "restricted@example.invalid", "Owner", "restricted-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Issue(ctx, user, "session", "Owner", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer srv.Close()
	call := func(method, path, token string, body any) (int, []byte) {
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(raw))
		r.Header.Set("X-OAP-CSRF", "1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "restricted-queue")
		if token == "" {
			r.AddCookie(&http.Cookie{Name: "oap-session", Value: session})
		} else {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, data
	}
	makeApp := func() domain.Application {
		m := manifest()
		m.Name = "restricted-" + domain.NewID()[:8]
		a, e := c.CreateApplication(ctx, m)
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	app := makeApp()
	issue := func(app domain.Application, actions []string) string {
		code, raw := call("POST", "/api/v1/access/credentials", "", map[string]any{"name": "Restricted Agent", "applicationId": app.ID, "scope": "operate", "actions": actions})
		if code != 201 {
			t.Fatalf("issue: %d %s", code, raw)
		}
		var v map[string]string
		json.Unmarshal(raw, &v)
		return v["token"]
	}
	restart := issue(app, []string{"restart"})
	for _, path := range []string{"/deployments", "/scale-down", "/rollbacks", "/future-action"} {
		code, _ := call("POST", "/api/v1/applications/"+app.ID+path, restart, map[string]any{})
		if code != 403 {
			t.Fatal("wrong action admitted", path, code)
		}
	}
	code, _ := call("PUT", "/api/v1/applications/"+app.ID, restart, map[string]any{"manifest": app.Manifest, "expectedVersion": app.Version})
	if code != 403 {
		t.Fatal("restart token edited definition", code)
	}
	code, _ = call("GET", "/api/v1/applications/"+app.ID, restart, nil)
	if code != 200 {
		t.Fatal("restricted read denied")
	}
	// Admission rejects mismatched pins and malformed allowlists before issuing a secret.
	for _, patch := range []map[string]any{{"targetId": "other"}, {"environment": "production"}, {"actions": []string{}}, {"actions": []string{"future"}}, {"actions": []string{"deploy", "deploy"}}, {"scope": "read", "actions": []string{"deploy"}}} {
		body := map[string]any{"name": "Invalid", "applicationId": app.ID, "scope": "operate", "actions": []string{"deploy"}}
		for k, v := range patch {
			body[k] = v
		}
		code, _ := call("POST", "/api/v1/access/credentials", "", body)
		if code != 422 {
			t.Fatal("invalid permissions issued", patch, code)
		}
	}
	p, err := service.Principal(ctx, restart)
	if err != nil || p.Permissions == nil || p.Permissions.TargetID != app.Manifest.TargetID || p.Permissions.Environment != app.Manifest.Environment {
		t.Fatal("pins lost", err)
	}
	// A trusted GitHub image-build path cannot bypass an agent's action restrictions.
	hook := source.Hook{ApplicationID: app.ID}
	if _, err = c.ReceiveSource(domain.WithPrincipal(ctx, p), hook, "scope-delivery", []byte("fixture"), source.Push{}); err != access.ErrDenied {
		t.Fatal("restart credential admitted source build", err)
	}
	// SQL here is a fixture fault injection; the platform exposes no agent DB access.
	for _, fault := range []string{"action", "target", "environment", "expired", "revoked"} {
		a := makeApp()
		token := issue(a, []string{"deploy"})
		principal, e := service.Principal(ctx, token)
		if e != nil {
			t.Fatal(e)
		}
		status, raw := call("POST", "/api/v1/applications/"+a.ID+"/deployments", token, map[string]any{})
		if status != 202 {
			t.Fatalf("enqueue %d %s", status, raw)
		}
		var d domain.Deployment
		if json.Unmarshal(raw, &d) != nil {
			t.Fatal("bad operation")
		}
		switch fault {
		case "action":
			_, e = c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET permissions=jsonb_set(permissions,'{actions}','[\"restart\"]') WHERE id=$1", principal.CredentialID)
		case "target":
			_, e = c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET permissions=jsonb_set(permissions,'{targetId}','\"other\"') WHERE id=$1", principal.CredentialID)
		case "environment":
			_, e = c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET permissions=jsonb_set(permissions,'{environment}','\"production\"') WHERE id=$1", principal.CredentialID)
		case "expired":
			_, e = c.Store.Pool.Exec(ctx, "UPDATE oap_credentials SET expires_at=now()-interval '1 second' WHERE id=$1", principal.CredentialID)
		case "revoked":
			status, _ = call("DELETE", "/api/v1/access/credentials/"+principal.CredentialID, "", nil)
			if status != 200 {
				t.Fatal("revoke failed")
			}
		}
		if e != nil {
			t.Fatal(e)
		}
		if fault == "target" || fault == "environment" {
			status, _ = call("GET", "/api/v1/applications/"+a.ID, token, nil)
			if status != 404 {
				t.Fatal("pin mismatch read allowed", fault, status)
			}
		}
		if e = c.Advance(ctx, d.ID); e != nil {
			t.Fatal(e)
		}
		d, e = c.Store.Deployment(ctx, d.ID)
		if e != nil || d.State != "attention" || f.deploys != 0 || len(f.resources) != 0 {
			t.Fatal("queued authority fault mutated provider", fault, e)
		}
		status, raw = call("GET", "/api/v1/access/audit", "", nil)
		if status != 200 || !bytes.Contains(raw, []byte(d.ID)) || !bytes.Contains(raw, []byte("attention")) || bytes.Contains(raw, []byte(token)) {
			t.Fatal("held operation not safely traceable")
		}
	}
	// Recover permission alone cannot attach a deploy-capable identity to existing work.
	recoveryToken := issue(app, []string{"recover"})
	recoveryPrincipal, _ := service.Principal(ctx, recoveryToken)
	ownerPrincipal, _ := service.Principal(ctx, session)
	d, e := c.Enqueue(domain.WithPrincipal(ctx, ownerPrincipal), app.ID, "owner-queued-operation")
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Recover(domain.WithPrincipal(ctx, recoveryPrincipal), d.ID, controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: d.UpdatedAt})
	if e != access.ErrDenied {
		t.Fatal("recovery-only credential adopted deployment", e)
	}
}
