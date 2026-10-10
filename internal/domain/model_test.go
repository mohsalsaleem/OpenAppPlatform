package domain

import "testing"

func valid() Manifest {
	return Manifest{Name: "hello", Environment: "staging", TargetID: "local", Components: []Component{{Name: "web", Image: "nginx:1.27-alpine", Port: 80}}}
}
func TestManifestValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Manifest)
	}{{"duplicate component", func(m *Manifest) { m.Components = append(m.Components, m.Components[0]) }}, {"unsupported strategy", func(m *Manifest) { m.Components[0].Strategy = "blue-green" }}, {"worker lifecycle", func(m *Manifest) { m.Components[0].Kind = "worker" }}, {"invalid port", func(m *Manifest) { m.Components[0].Port = 70000 }}, {"invalid slug", func(m *Manifest) { m.Name = "Hello world" }}, {"adoption replicas", func(m *Manifest) { m.Components[0].ResourceID = "external"; m.Components[0].Instances = 2 }}, {"no components", func(m *Manifest) { m.Components = nil }}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := valid()
			tt.change(&m)
			if m.Validate() == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	m := valid()
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	if m.Components[0].Instances != 1 || m.Components[0].Strategy != "standard" {
		t.Fatal("defaults missing")
	}
}

func TestRuntimeConfigurationValidation(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Manifest)
	}{
		{"invalid variable", func(m *Manifest) { m.Components[0].Env = map[string]string{"BAD=KEY": "x"} }},
		{"NUL value", func(m *Manifest) { m.Components[0].Env = map[string]string{"KEY": "\x00"} }},
		{"unknown service", func(m *Manifest) { m.Components[0].Services = map[string]string{"API_URL": "missing"} }},
		{"duplicate source", func(m *Manifest) {
			m.Components[0].Env = map[string]string{"API_URL": "x"}
			m.Components[0].Services = map[string]string{"API_URL": "web"}
		}},
		{"adopted variables", func(m *Manifest) {
			m.Components[0].ResourceID = "external"
			m.Components[0].Env = map[string]string{"MODE": "test"}
		}},
		{"adopted service target", func(m *Manifest) {
			m.Components = append(m.Components, Component{Name: "api", Image: "image:v1", Port: 8080, ResourceID: "external"})
			m.Components[0].Services = map[string]string{"API_URL": "api"}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := valid()
			tt.edit(&m)
			if m.Validate() == nil {
				t.Fatal("invalid runtime accepted")
			}
		})
	}
	m := valid()
	m.Components = append(m.Components, Component{Name: "api", Image: "image:v1", Port: 8080})
	m.Components[0].Env = map[string]string{"MODE": "preview"}
	m.Components[0].Services = map[string]string{"API_URL": "api"}
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	runtime, e := RuntimeComponent(m, "web")
	if e != nil {
		t.Fatal(e)
	}
	if runtime.Env["API_URL"] != "http://api:8080" || runtime.Env["MODE"] != "preview" {
		t.Fatal(runtime.Env)
	}
	runtime.Env["MODE"] = "changed"
	if m.Components[0].Env["MODE"] != "preview" {
		t.Fatal("runtime resolution mutated the definition")
	}
}

func TestExplicitServiceEndpointsValidateAndResolve(t *testing.T) {
	m := valid()
	m.Components = append(m.Components, Component{Name: "api", Image: "image:v1", Port: 8080, ResourceID: "adopted"})
	m.Components[0].Services = map[string]string{"API_URL": "api"}
	m.Components[0].ServiceEndpoints = map[string]string{"api": "https://api.example.com/base"}
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	runtime, e := RuntimeComponent(m, "web")
	if e != nil || runtime.Env["API_URL"] != "https://api.example.com/base" {
		t.Fatal(runtime, e)
	}
	for _, endpoint := range []string{"https://user:pass@example.com", "https://example.com/?token=x", "file:///etc/passwd", "https://example.com/#secret"} {
		m.Components[0].ServiceEndpoints["api"] = endpoint
		if m.Validate() == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}

func TestObserveOnlySourceManifest(t *testing.T) {
	m := Manifest{Name: "observed", Environment: "staging", TargetID: "fixture", Components: []Component{{Name: "api", Management: "observe", ResourceID: "source", Port: 0}}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	m.Components[0].Management = ""
	if err := m.Validate(); err == nil {
		t.Fatal("managed source without image accepted")
	}
	m.Components[0].Management = "observe"
	m.Components[0].Env = map[string]string{"SECRET": "value"}
	if err := m.Validate(); err == nil {
		t.Fatal("observe-only runtime configuration accepted")
	}
}
