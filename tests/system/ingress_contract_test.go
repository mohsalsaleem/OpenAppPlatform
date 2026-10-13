package system_test

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"testing"
)

func TestIngressAdmissionFailsClosedWithoutTargetCapability(t *testing.T) {
	adapter := &fake{resources: map[string]operator.Resource{}}
	c, server := setup(t, adapter)
	proposed := manifest()
	proposed.Ingress = &domain.IngressDefinition{Domain: "app.example.test", HTTPS: true, Routes: []domain.IngressRoute{{Path: "/", Component: "web"}}}
	status, raw := request(t, server, "POST", "/api/v1/applications", "", proposed)
	if status != 422 || !strings.Contains(string(raw), "ingress is not enabled") {
		t.Fatalf("unsupported ingress create %d", status)
	}
	applications, err := c.Store.Applications(context.Background())
	if err != nil || len(applications) != 0 {
		t.Fatal("rejected ingress persisted application", err)
	}
	plain := manifest()
	status, raw = request(t, server, "POST", "/api/v1/applications", "", plain)
	if status != 201 {
		t.Fatal(status)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	status, raw = request(t, server, "PUT", "/api/v1/applications/"+app.ID, "", map[string]any{"manifest": proposed, "expectedVersion": app.Version})
	if status != 422 || !strings.Contains(string(raw), "ingress is not enabled") {
		t.Fatalf("unsupported ingress update %d", status)
	}
	current, err := c.Store.Application(context.Background(), app.ID)
	if err != nil || current.Version != app.Version || current.Manifest.Ingress != nil {
		t.Fatal("rejected ingress update changed definition", err)
	}
	if adapter.deploys != 0 || len(adapter.resources) != 0 {
		t.Fatal("unsupported ingress mutated runtime")
	}
}
