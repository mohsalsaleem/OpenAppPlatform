package domain

import (
	"encoding/json"
	"testing"
)

func TestIngressSpecificationValidatesExportsAndDiffs(t *testing.T) {
	m := Manifest{Name: "example", Environment: "staging", TargetID: "docker", Components: []Component{{Name: "web", Kind: "web", Port: 8080, Image: "example/web:latest", Instances: 1}, {Name: "api", Kind: "web", Port: 8080, Image: "example/api:latest", Instances: 3}}}
	before := m
	m.Ingress = &IngressDefinition{Domain: "app.example.test", HTTPS: true, Routes: []IngressRoute{{Path: "/", Component: "web"}, {Path: "/api", Component: "api", StripPrefix: true}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	spec := ExportSpecification(m)
	raw, _ := json.Marshal(spec)
	var roundTrip ApplicationSpecification
	if json.Unmarshal(raw, &roundTrip) != nil || roundTrip.Validate() != nil || roundTrip.Spec.Ingress.Domain != m.Ingress.Domain {
		t.Fatal("ingress export failed")
	}
	diff := SpecificationDiff(Application{Manifest: before, Version: 1}, m)
	found := false
	for _, change := range diff.Changes {
		if change.Field == "application ingress" {
			found = true
		}
	}
	if !found {
		t.Fatal("ingress change missing from diff")
	}
	m.Ingress.Routes[0].Component = "worker"
	if m.Validate() == nil {
		t.Fatal("unknown route accepted")
	}
}
