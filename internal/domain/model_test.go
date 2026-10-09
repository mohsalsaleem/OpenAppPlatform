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
