package config

import (
	"fmt"
	"testing"
)

func TestTargetsAllowDockerWithoutCoolifyPlacement(t *testing.T) {
	x, e := Targets([]byte(`[{"id":"docker-local","name":"Local","operator":"docker","url":"unix:///var/run/docker.sock","environment":"staging","settings":{"pullPolicy":"never"}}]`))
	if e != nil || len(x) != 1 || x[0].ProjectID != "" {
		t.Fatal(x, e)
	}
}
func TestTargetsRejectUnknownAndDuplicateFields(t *testing.T) {
	for _, data := range []string{`[{"id":"local","password":"secret"}]`, `[] {}`, `[{"id":"local","name":"A","operator":"docker","url":"unix:///s","environment":"staging"},{"id":"local","name":"B","operator":"docker","url":"unix:///s","environment":"staging"}]`} {
		if _, e := Targets([]byte(data)); e == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestCoolifyReferencesCannotUseControllerSecrets(t *testing.T) {
	for _, ref := range []string{"DATABASE_URL", "OAP_SETUP_TOKEN", "OAP_API_TOKEN", "OAP_BUILD_TOKEN", "bad\nref", ""} {
		input := fmt.Sprintf(`[{"id":"coolify","name":"Coolify","operator":"coolify","url":"https://operator.invalid","environment":"staging","projectId":"p","serverId":"s","tokenEnv":%q}]`, ref)
		if _, e := Targets([]byte(input)); e == nil {
			t.Fatal("unsafe credential reference accepted", ref)
		}
	}
}
