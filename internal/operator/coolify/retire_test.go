package coolify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func TestRetirementPreservesResourcesAndRequiresScopedManagedOwnership(t *testing.T) {
	owner := "OpenAppPlatform:" + strings.Repeat("a", 32) + ":api"
	description, strategy, status := owner, "dockerimage", "running:healthy"
	stops := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			w.Write([]byte(`{"applications":[{"uuid":"owned"}]}`))
		case "/api/v1/applications/owned":
			json.NewEncoder(w).Encode(map[string]string{"uuid": "owned", "description": description, "build_pack": strategy, "status": status})
		case "/api/v1/applications/owned/stop":
			if r.Method != "POST" || r.URL.Query().Get("docker_cleanup") != "false" {
				t.Error("retirement must stop without Docker cleanup")
			}
			stops++
			w.Write([]byte(`{"message":"Application stopping request queued."}`))
		default:
			t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(domain.Target{URL: server.URL, ProjectID: "p", Environment: "staging"}, "token")
	ctx := context.Background()
	if _, err := client.Retire(ctx, "foreign", owner); err == nil || stops != 0 {
		t.Fatal("foreign retirement allowed")
	}
	description = "different-owner"
	if _, err := client.Retire(ctx, "owned", owner); err == nil || stops != 0 {
		t.Fatal("changed ownership allowed")
	}
	description, strategy = owner, "nixpacks"
	if _, err := client.Retire(ctx, "owned", owner); err == nil || stops != 0 {
		t.Fatal("native source retirement allowed")
	}
	strategy = "dockerimage"
	remote, err := client.Retire(ctx, "owned", owner)
	if err != nil || remote != "owned" || stops != 1 {
		t.Fatal("scoped stop failed", err)
	}
	if _, err = client.ObserveRetirement(ctx, "different-operation", "owned", owner); err == nil {
		t.Fatal("wrong confirmation reference accepted")
	}
	observed, err := client.ObserveRetirement(ctx, remote, "owned", owner)
	if err != nil || observed.State != "running" {
		t.Fatal("queued stop was falsely confirmed", err)
	}
	status = "exited:unhealthy"
	observed, err = client.ObserveRetirement(ctx, remote, "owned", owner)
	if err != nil || observed.State != "succeeded" || stops != 1 {
		t.Fatal("stopped observation redispatched or failed", err)
	}
	status = "unknown"
	if _, err = client.ObserveRetirement(ctx, remote, "owned", owner); err == nil {
		t.Fatal("unknown native state confirmed stopped")
	}
	status, description = "exited", "different-owner"
	if _, err = client.ObserveRetirement(ctx, remote, "owned", owner); err == nil {
		t.Fatal("recovery ignored changed ownership")
	}
}
