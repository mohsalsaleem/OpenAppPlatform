package coolify

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeHTTPHealthConfigurationIsScopedAndReadBack(t *testing.T) {
	fields := map[string]any{"uuid": "owned", "name": "web", "description": "OpenAppPlatform:app:web", "build_pack": "dockerimage", "ports_exposes": "8080"}
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			json.NewEncoder(w).Encode(map[string]any{"applications": []map[string]any{fields}})
		case "/api/v1/applications/owned":
			if r.Method == "PATCH" {
				writes++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				for k, v := range body {
					fields[k] = v
				}
				w.Write([]byte(`{}`))
			} else {
				json.NewEncoder(w).Encode(fields)
			}
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	comp := domain.Component{Name: "web", Image: "image:v1", Port: 8080, HealthCheck: &domain.HealthCheck{Mode: "http", Path: "/health/custom", IntervalSeconds: 5, TimeoutSeconds: 2, Retries: 2, StartPeriodSeconds: 10}}
	if _, err := c.Ensure(context.Background(), operator.Spec{Name: "web", Ownership: "OpenAppPlatform:app:web", Component: comp}); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckHealthCheck(context.Background(), "owned", comp, true); err != nil {
		t.Fatal(err)
	}
	if fields["health_check_host"] != "127.0.0.1" || fields["health_check_type"] != "http" || writes != 1 {
		t.Fatal("HTTP scope was not fixed", fields)
	}
	fields["health_check_path"] = "/wrong"
	if err := c.CheckHealthCheck(context.Background(), "owned", comp, false); err == nil {
		t.Fatal("drift accepted")
	}
	if err := c.CheckHealthCheck(context.Background(), "foreign", comp, false); err == nil {
		t.Fatal("foreign resource accepted")
	}
	comp.HealthCheck = &domain.HealthCheck{Mode: "image"}
	if _, err := c.Ensure(context.Background(), operator.Spec{Name: "web", Ownership: "OpenAppPlatform:app:web", Component: comp}); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckHealthCheck(context.Background(), "owned", comp, false); err != nil {
		t.Fatal(err)
	}
	if fields["health_check_enabled"] != false {
		t.Fatal("HTTP override was not removed")
	}
}
