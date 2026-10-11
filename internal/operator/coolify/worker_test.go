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

func TestWorkerCreationHasNoDomainAndDriftBlocksConfiguration(t *testing.T) {
	fields := map[string]any{}
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			rows := []map[string]any{}
			if len(fields) > 0 {
				rows = append(rows, fields)
			}
			json.NewEncoder(w).Encode(map[string]any{"applications": rows})
		case "/api/v1/applications/dockerimage":
			json.NewDecoder(r.Body).Decode(&fields)
			writes++
			fields["uuid"] = "owned"
			fields["build_pack"] = "dockerimage"
			fields["fqdn"] = ""
			json.NewEncoder(w).Encode(map[string]string{"uuid": "owned"})
		case "/api/v1/applications/owned":
			if r.Method == "PATCH" {
				writes++
				var patch map[string]any
				json.NewDecoder(r.Body).Decode(&patch)
				for k, v := range patch {
					fields[k] = v
				}
			}
			json.NewEncoder(w).Encode(fields)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(domain.Target{URL: srv.URL, ProjectID: "p", Environment: "staging"}, "token")
	comp := domain.Component{Name: "worker", Kind: "worker", Image: "image:v1", HealthCheck: &domain.HealthCheck{Mode: "image"}}
	spec := operator.Spec{Name: "worker", Ownership: "OpenAppPlatform:app:worker", Component: comp}
	if _, err := c.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if fields["autogenerate_domain"] != false || fields["health_check_enabled"] != false || fields["ports_exposes"] != "80" || writes != 1 {
		t.Fatal("worker HTTP routing was not disabled")
	}
	if err := c.CheckHealthCheck(context.Background(), "owned", comp, false); err != nil {
		t.Fatal(err)
	}
	fields["fqdn"] = "https://changed.example.com"
	if _, err := c.Ensure(context.Background(), spec); err == nil || writes != 1 {
		t.Fatal("worker native route drift mutated resource")
	}
	if err := c.CheckHealthCheck(context.Background(), "owned", comp, true); err == nil {
		t.Fatal("worker route drift allowed dispatch")
	}
}
