package domain

import "testing"

func TestWorkerUsesImageReadinessAndRejectsHTTPAndPorts(t *testing.T) {
	m := valid()
	m.Components[0].Kind = "worker"
	m.Components[0].Port = 0
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.Components[0].HealthCheck == nil || m.Components[0].HealthCheck.Mode != "image" {
		t.Fatal("worker inherited HTTP health")
	}
	for _, change := range []func(*Component){func(c *Component) { c.Port = 80 }, func(c *Component) { c.HostPort = 1234 }, func(c *Component) { c.HealthCheck = &HealthCheck{Mode: "http", Path: "/ready"} }, func(c *Component) { c.ResourceID = "native" }, func(c *Component) { c.Kind = "job" }} {
		invalid := m
		invalid.Components = append([]Component(nil), m.Components...)
		change(&invalid.Components[0])
		if invalid.Validate() == nil {
			t.Fatal("unsupported worker settings accepted")
		}
	}
	caller := Component{Name: "web", Image: "image:v1", Port: 80, Services: map[string]string{"WORKER_URL": "web"}}
	m.Components[0].Name = "worker"
	caller.Services["WORKER_URL"] = "worker"
	m.Components = append(m.Components, caller)
	if m.Validate() == nil {
		t.Fatal("worker accepted as an HTTP endpoint")
	}
}
