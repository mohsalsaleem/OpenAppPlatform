package config

import "testing"

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
