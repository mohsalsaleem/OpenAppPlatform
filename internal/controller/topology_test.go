package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestComponentRemovalPreservesConfigurationAndOwnedData(t *testing.T) {
	current := domain.Manifest{Name: "app", Environment: "staging", TargetID: "docker", Components: []domain.Component{{Name: "web", Kind: "web", Image: "web:fixture", Port: 8080, Instances: 1}, {Name: "worker", Kind: "worker", Image: "worker:fixture", Instances: 1, Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}}}}
	if err := current.Validate(); err != nil {
		t.Fatal(err)
	}
	desired := current
	desired.Components = append([]domain.Component(nil), current.Components[:1]...)
	removed, err := desiredComponentRemoval(current, desired)
	if err != nil || removed.Name != "worker" || len(removed.Volumes) != 1 {
		t.Fatal(removed, err)
	}
	release := domain.Deployment{Operation: "remove-component", Manifest: desired, Steps: []domain.Step{{Component: "worker", Ordinal: 1, Action: "retire"}}}
	claims, err := releaseClaims(release)
	if err != nil || claims["worker"] != "write" || claims["web"] != "read" {
		t.Fatal(claims, err)
	}
	desired.Components[0].Image = "different:fixture"
	if _, err = desiredComponentRemoval(current, desired); err == nil {
		t.Fatal("removal changed unrelated image")
	}
	desired.Components[0].Image = "web:fixture"
	current.Components[1].Management = "observe"
	current.Components[1].ResourceID = "external"
	if _, err = desiredComponentRemoval(current, desired); err == nil {
		t.Fatal("external resource could be removed")
	}
}
