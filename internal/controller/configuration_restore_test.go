package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestConfigurationRestoreKeepsImagesAndUsesExplicitImageHealthReset(t *testing.T) {
	source := domain.Manifest{Name: "app", Environment: "staging", TargetID: "target", Components: []domain.Component{{Name: "web", Kind: "web", Image: "old:image", Port: 80, Instances: 1, Strategy: "standard", Env: map[string]string{"MODE": "old"}}}}
	current := source
	current.Components = append([]domain.Component(nil), source.Components...)
	current.Components[0].Image = "current:image"
	current.Components[0].Port = 8080
	current.Components[0].HealthCheck = &domain.HealthCheck{Mode: "http", Path: "/ready"}
	steps := []domain.Step{{Component: "web", Ordinal: 1, Phase: "succeeded", Observed: "running:healthy", ResourceID: "resource"}}
	desired, err := restoreConfigurationDefinition(current, source, steps)
	if err != nil {
		t.Fatal(err)
	}
	if desired.Components[0].Image != "current:image" || desired.Components[0].Port != 80 || desired.Components[0].HealthCheck.Mode != "image" {
		t.Fatalf("wrong restoration: %+v", desired)
	}
	desired.Components[0].Env["MODE"] = "changed"
	if source.Components[0].Env["MODE"] != "old" {
		t.Fatal("mutated frozen snapshot")
	}
	for _, test := range []struct {
		name   string
		change func(*domain.Manifest, *[]domain.Step)
	}{
		{"replica change", func(m *domain.Manifest, _ *[]domain.Step) { m.Components[0].Instances = 2 }},
		{"observed ownership", func(m *domain.Manifest, _ *[]domain.Step) { m.Components[0].Management = "observe" }},
		{"target migration", func(m *domain.Manifest, _ *[]domain.Step) { m.TargetID = "other" }},
		{"unverified replica", func(_ *domain.Manifest, s *[]domain.Step) { (*s)[0].Observed = "running" }},
		{"duplicate verification", func(_ *domain.Manifest, s *[]domain.Step) { *s = append(*s, (*s)[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := current
			m.Components = append([]domain.Component(nil), current.Components...)
			s := append([]domain.Step(nil), steps...)
			test.change(&m, &s)
			if _, err := restoreConfigurationDefinition(m, source, s); err == nil {
				t.Fatal("unsafe restoration accepted")
			}
		})
	}
}
