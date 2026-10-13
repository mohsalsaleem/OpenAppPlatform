package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestDesiredScaleDownIsOneExactCountChange(t *testing.T) {
	current := domain.Manifest{Name: "app", Environment: "staging", TargetID: "docker", Components: []domain.Component{{Name: "web", Kind: "web", Instances: 3, Port: 8080, Image: "example/web:latest"}}}
	proposed := current
	proposed.Components = append([]domain.Component(nil), current.Components...)
	proposed.Components[0].Instances = 1
	request, err := desiredScaleDown(current, proposed)
	if err != nil || request.Component != "web" || request.Instances != 1 {
		t.Fatal(request, err)
	}
	proposed.Components[0].Image = "example/web:changed"
	if _, err = desiredScaleDown(current, proposed); err == nil {
		t.Fatal("unrelated image change accepted")
	}
	proposed.Components[0] = current.Components[0]
	if _, err = desiredScaleDown(current, proposed); err == nil {
		t.Fatal("no change accepted")
	}
	proposed.Components[0].Instances = 0
	if _, err = desiredScaleDown(current, proposed); err == nil {
		t.Fatal("remove last instance accepted")
	}
}
