package httpapi

import "testing"

func TestAgentMutationAllowlistIsExplicit(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/applications/a/deployments": "deploy", "/api/v1/applications/a/restarts": "restart",
		"/api/v1/applications/a/scale-down": "scale-down", "/api/v1/applications/a/rollbacks": "rollback",
		"/api/v1/applications/a/configuration-restores": "configure", "/api/v1/applications/a/databases": "configure",
		"/api/v1/deployments/d/recover": "recover", "/api/v1/deployments/d/control": "control",
		"/api/v1/applications/a/future-action": "", "/api/v1/applications/a/restarts/extra": "",
	} {
		if got := agentRequestAction("POST", path); got != want {
			t.Fatalf("%s: %s != %s", path, got, want)
		}
	}
	if agentRequestAction("PUT", "/api/v1/applications/a") != "configure" || agentRequestAction("GET", "/api/v1/applications/a/restarts") != "" {
		t.Fatal("method boundary lost")
	}
}
