package coolify

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageReferences(t *testing.T) {
	for _, tt := range []struct{ in, name, tag string }{{"nginx:1.27", "nginx", "1.27"}, {"registry:5000/team/api:v1", "registry:5000/team/api", "v1"}, {"registry:5000/team/api", "registry:5000/team/api", "latest"}, {"nginx@sha256:abc", "nginx@sha256", "abc"}} {
		n, tag := splitImage(tt.in)
		if n != tt.name || tag != tt.tag {
			t.Fatalf("%s: %s %s", tt.in, n, tag)
		}
	}
}
func TestClientScopesAndDoesNotLeakCredentials(t *testing.T) {
	const token = "sensitive-test-token"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing auth")
		}
		calls++
		if r.URL.Path == "/api/v1/projects/p/staging" {
			json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]any{{"uuid": "owned", "name": "web", "description": "OpenAppPlatform:app:web", "status": "running:healthy"}}})
			return
		}
		http.Error(w, token, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, e := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, token)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Inspect(context.Background(), "outside"); e == nil {
		t.Fatal("out of scope accepted")
	}
	if calls != 1 {
		t.Fatal("out of scope resource requested")
	}
	_, e = c.Inspect(context.Background(), "owned")
	if e == nil || strings.Contains(e.Error(), token) {
		t.Fatal("unsafe error", e)
	}
}
func TestEnsureRefusesOwnershipCollision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("must not mutate collision")
		}
		json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]string{{"uuid": "u", "name": "same", "description": "another owner"}}})
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	_, e := c.Ensure(context.Background(), operator.Spec{Name: "same", Ownership: "OpenAppPlatform:app:web"})
	if e == nil {
		t.Fatal("collision accepted")
	}
}
func TestRedirectCannotForwardAuthorization(t *testing.T) {
	hits := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL}, "secret")
	_, e := c.Discover(context.Background())
	if e == nil || hits != 0 {
		t.Fatal("followed redirect")
	}
}

func TestDigestNormalizationMatchesCoolifyStorage(t *testing.T) {
	r := resource(application{Image: "registry/team/api@sha256", Tag: "abcdef"})
	if r.Image != "registry/team/api@sha256:abcdef" {
		t.Fatalf("digest lost: %s", r.Image)
	}
}

func TestObserveAcceptsStringApplicationIDAndRequiresProviderCompletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]string{{"uuid": "u"}}})
		case "/api/v1/applications/u":
			json.NewEncoder(w).Encode(map[string]string{"id": "79", "uuid": "u", "status": "running:healthy"})
		case "/api/v1/deployments/applications/u":
			json.NewEncoder(w).Encode(map[string]any{"count": 1, "deployments": []map[string]string{{"deployment_uuid": "d", "status": "finished", "application_id": "79"}}})
		default:
			t.Error("unexpected path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	got, e := c.Observe(context.Background(), "d", "u")
	if e != nil || got.State != "succeeded" || got.ResourceStatus != "running:healthy" {
		t.Fatalf("observation %v %v", got, e)
	}
}

func TestObserveRejectsUnrelatedProviderDeployment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			w.Write([]byte(`{"applications":[{"uuid":"u"}]}`))
		case "/api/v1/applications/u":
			w.Write([]byte(`{"uuid":"u","status":"running:healthy"}`))
		case "/api/v1/deployments/applications/u":
			w.Write([]byte(`{"count":1,"deployments":[{"deployment_uuid":"owned-operation","status":"finished"}]}`))
		default:
			t.Errorf("unexpected global lookup: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	if _, e := c.Observe(context.Background(), "unrelated-operation", "u"); e == nil {
		t.Fatal("unrelated provider deployment accepted")
	}
}

func TestObservePaginatesBoundedResourceHistory(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			w.Write([]byte(`{"applications":[{"uuid":"u"}]}`))
		case "/api/v1/applications/u":
			w.Write([]byte(`{"uuid":"u","status":"running:healthy"}`))
		case "/api/v1/deployments/applications/u":
			calls++
			deployments := []map[string]string{}
			for i := 0; i < 20; i++ {
				deployments = append(deployments, map[string]string{"deployment_uuid": "other", "status": "finished"})
			}
			if r.URL.Query().Get("skip") == "20" {
				deployments[0]["deployment_uuid"] = "older"
				deployments[0]["status"] = "in_progress"
			}
			json.NewEncoder(w).Encode(map[string]any{"count": 200, "deployments": deployments})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	got, e := c.Observe(context.Background(), "older", "u")
	if e != nil || got.State != "running" || calls != 2 {
		t.Fatalf("pagination %v %v calls=%d", got, e, calls)
	}
	calls = 0
	if _, e = c.Observe(context.Background(), "absent", "u"); e == nil || calls != 5 {
		t.Fatalf("unbounded history search: %v calls=%d", e, calls)
	}
}

func TestRestartScopesResourceAndReturnsProviderOperation(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			w.Write([]byte(`{"applications":[{"uuid":"u"}]}`))
		case "/api/v1/applications/u":
			w.Write([]byte(`{"uuid":"u","status":"running:healthy"}`))
		case "/api/v1/applications/u/restart":
			if r.Method != "POST" {
				t.Fatal(r.Method)
			}
			calls++
			w.Write([]byte(`{"deployment_uuid":"restart-operation"}`))
		default:
			t.Errorf("unexpected mutation %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	if _, e := c.Restart(context.Background(), "foreign"); e == nil || calls != 0 {
		t.Fatal("foreign resource restart allowed")
	}
	remote, e := c.Restart(context.Background(), "u")
	if e != nil || remote != "restart-operation" || calls != 1 {
		t.Fatalf("restart %s %v %d", remote, e, calls)
	}
}
