package httpapi

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"testing"
)

func TestApplicationActionEligibility(t *testing.T) {
	app := domain.Application{ID: "app", Manifest: domain.Manifest{TargetID: "docker", Environment: "staging", Components: []domain.Component{{Name: "api", Kind: "web"}, {Name: "native", Kind: "web", Management: "observe"}, {Name: "db", Kind: "database"}}}}
	principal := domain.Principal{WorkspaceID: "default", Role: "owner", Scope: "operate", ApplicationID: "app", Permissions: &domain.AgentPermissions{TargetID: "docker", Environment: "staging", Actions: []string{"deploy"}}}
	caps := operator.Capabilities{Standard: true, Restart: true}
	actions := applicationActions(app, caps, principal, false)
	if !actions["api"]["deploy"].Allowed || actions["api"]["restart"].Allowed || actions["native"]["deploy"].Allowed || actions["db"]["deploy"].Allowed {
		t.Fatal("permission or ownership ignored")
	}
	principal.Permissions.TargetID = "other"
	if applicationActions(app, caps, principal, false)["api"]["deploy"].Allowed {
		t.Fatal("pin ignored")
	}
	if applicationActions(app, operator.Capabilities{}, principal, true)["api"]["deploy"].Allowed {
		t.Fatal("runtime capability ignored")
	}
}
