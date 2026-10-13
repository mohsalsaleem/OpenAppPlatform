package domain

import "testing"

func TestAgentPermissionBoundaries(t *testing.T) {
	p := Principal{WorkspaceID: "default", Role: "owner", Scope: "operate", ApplicationID: "app", Permissions: &AgentPermissions{TargetID: "docker", Environment: "staging", Actions: []string{"restart"}}}
	if !p.Allows("app", "restart", "docker", "staging") {
		t.Fatal("allowed restart denied")
	}
	for _, v := range [][4]string{{"other", "restart", "docker", "staging"}, {"app", "deploy", "docker", "staging"}, {"app", "restart", "other", "staging"}, {"app", "restart", "docker", "production"}, {"app", "future-action", "docker", "staging"}} {
		if p.Allows(v[0], v[1], v[2], v[3]) {
			t.Fatal("permission broadened", v)
		}
	}
	p.Scope = "read"
	if p.Allows("app", "restart", "docker", "staging") || !p.Reads("app", "docker", "staging") {
		t.Fatal("read scope changed")
	}
	p.Permissions = nil
	p.Scope = "operate"
	if !p.Allows("app", "configure", "docker", "production") {
		t.Fatal("legacy permissions changed")
	}
}
