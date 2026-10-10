package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestRollbackConfigurationIgnoresOnlyImagesAndEquivalentPolicyDefaults(t *testing.T) {
	a := domain.Manifest{Name: "app", Environment: "staging", TargetID: "target", Components: []domain.Component{{Name: "web", Image: "old", Port: 80, Instances: 1}, {Name: "api", Image: "old", Port: 80, Instances: 1}}}
	b := a
	b.Components = append([]domain.Component(nil), a.Components...)
	b.Components[0].Image = "new"
	b.Components[0].Readiness = &domain.ReadinessPolicy{TimeoutSeconds: 900}
	if !sameRollbackConfiguration(a, b) {
		t.Fatal("image change or equivalent readiness default rejected")
	}
	b.Components[0].Env = map[string]string{"MIGRATION_MODE": "different"}
	if sameRollbackConfiguration(a, b) {
		t.Fatal("runtime config change accepted")
	}
	b.Components[0].Env = nil
	b.Components[0].Instances = 2
	if sameRollbackConfiguration(a, b) {
		t.Fatal("topology change accepted")
	}
}
