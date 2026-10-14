package domain

import (
	"reflect"
	"testing"
)

func TestJobsValidateAndOrderBeforeDependents(t *testing.T) {
	m := Manifest{Name: "jobs", TargetID: "docker", Environment: "staging", Components: []Component{{Name: "api", Kind: "web", Image: "api", Port: 8080, DependsOn: []string{"migrate"}}, {Name: "migrate", Kind: "job", Image: "migration", Readiness: &ReadinessPolicy{TimeoutSeconds: 30}}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	steps := InitialSteps(m)
	if len(steps) != 2 || steps[0].Component != "migrate" || m.Components[1].Instances != 1 {
		t.Fatal("migration ordering", steps)
	}
	for _, mutate := range []func(*Component){
		func(c *Component) { c.Port = 80 }, func(c *Component) { c.Instances = 2 }, func(c *Component) { c.ResourceID = "adopted" },
		func(c *Component) { c.HealthCheck = &HealthCheck{Mode: "image"} }, func(c *Component) { c.Readiness.RequireHealthy = true },
		func(c *Component) { c.Volumes = []VolumeMount{{Name: "data", MountPath: "/data"}} },
	} {
		changed := m
		changed.Components = append([]Component(nil), m.Components...)
		changed.Components[1].Readiness = &ReadinessPolicy{TimeoutSeconds: 30}
		mutate(&changed.Components[1])
		if err := changed.Validate(); err == nil {
			t.Fatal("invalid job accepted", changed.Components[1])
		}
	}
}

func TestSelectedReleaseIncludesTransitiveJobsWithoutOtherWorkloads(t *testing.T) {
	m := Manifest{Components: []Component{{Name: "web", DependsOn: []string{"api"}}, {Name: "api", DependsOn: []string{"migrate"}}, {Name: "migrate", Kind: "job", DependsOn: []string{"seed"}}, {Name: "seed", Kind: "job"}}}
	names, err := ExpandJobDependencies(m, []string{"web"})
	if err != nil || !reflect.DeepEqual(names, []string{"migrate", "seed", "web"}) {
		t.Fatal(names, err)
	}
	if _, err = ExpandJobDependencies(m, []string{"web", "web"}); err == nil {
		t.Fatal("duplicate selection accepted")
	}
	if _, err = ExpandJobDependencies(m, []string{"missing"}); err == nil {
		t.Fatal("unknown selection accepted")
	}
}
