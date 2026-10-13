package ingress

import (
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"testing"
	"time"
)

func fixture() (domain.Manifest, Definition, []Member, Options, time.Time) {
	now := time.Now()
	m := domain.Manifest{TargetID: "docker", Components: []domain.Component{{Name: "web", Kind: "web", Port: 8080, Instances: 1}, {Name: "api", Kind: "web", Port: 8080, Instances: 3}, {Name: "worker", Kind: "worker", Instances: 1}}}
	d := Definition{Domain: "app.example.test", Routes: []Route{{Path: "/", Component: "web"}, {Path: "/api", Component: "api", StripPrefix: true}}}
	members := []Member{{ApplicationID: "app", TargetID: "docker", Component: "web", Ordinal: 1, Address: "172.20.0.2:8080", Ready: true, ObservedAt: now}}
	for i := 1; i <= 3; i++ {
		members = append(members, Member{ApplicationID: "app", TargetID: "docker", Component: "api", Ordinal: i, Address: "172.20.0." + string(rune('2'+i)) + ":8080", Ready: true, ObservedAt: now})
	}
	return m, d, members, Options{AdminSocket: "/run/oap-ingress/admin.sock", HTTPPort: 8080, HTTPSPort: 8443}, now
}
func TestCompileDeterministicPathsMembershipAndTLS(t *testing.T) {
	m, d, members, o, now := fixture()
	members[3].Ready = false
	first, err := Compile("app", m, d, members, o, now)
	if err != nil {
		t.Fatal(err)
	}
	d.Routes[0], d.Routes[1] = d.Routes[1], d.Routes[0]
	members[0], members[1] = members[1], members[0]
	second, err := Compile("app", m, d, members, o, now)
	if err != nil || first.Hash() != second.Hash() {
		t.Fatal("non-deterministic config", err)
	}
	var config map[string]any
	json.Unmarshal(first.JSON(), &config)
	routes := config["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["oap"].(map[string]any)["routes"].([]any)
	path := routes[0].(map[string]any)["match"].([]any)[0].(map[string]any)["path"]
	if string(mustJSON(path)) != `["/api","/api/*"]` {
		t.Fatal(path)
	}
	raw := string(first.JSON())
	if strings.Contains(raw, "172.20.0.5") || !strings.Contains(raw, `"strip_path_prefix":"/api"`) {
		t.Fatal("bad membership or rewrite")
	}
	copy := first.JSON()
	copy[0] = 'x'
	if first.JSON()[0] != '{' {
		t.Fatal("mutable candidate")
	}
	d.HTTPS = true
	o.InternalCA = true
	tls, err := Compile("app", m, d, members, o, now)
	if err != nil || !strings.Contains(string(tls.JSON()), `"module":"internal"`) {
		t.Fatal("fixture TLS", err)
	}
	o.InternalCA = false
	tls, err = Compile("app", m, d, members, o, now)
	if err != nil || !strings.Contains(string(tls.JSON()), "acme-v02.api.letsencrypt.org") {
		t.Fatal("public TLS", err)
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func TestCompileRejectsUnsafeOrStaleInputs(t *testing.T) {
	cases := []struct {
		name   string
		change func(*domain.Manifest, *Definition, *[]Member, *Options)
	}{
		{"worker route", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { d.Routes[0].Component = "worker" }},
		{"unknown component", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { d.Routes[0].Component = "unknown" }},
		{"observed", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) {
			m.Components[0].Management = "observe"
		}},
		{"hostport", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { m.Components[0].HostPort = 8080 }},
		{"duplicate route", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { d.Routes[1].Path = "/" }},
		{"wildcard", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { d.Domain = "*.example.test" }},
		{"path expression", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { d.Routes[0].Path = "/api*" }},
		{"encoded path", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { d.Routes[0].Path = "/api%2Fsecret" }},
		{"sibling", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { (*b)[0].ApplicationID = "other" }},
		{"target", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { (*b)[0].TargetID = "other" }},
		{"stale", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) {
			(*b)[0].ObservedAt = time.Now().Add(-time.Minute)
		}},
		{"future", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) {
			(*b)[0].ObservedAt = time.Now().Add(time.Hour)
		}},
		{"unready", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { (*b)[0].Ready = false }},
		{"duplicate ordinal", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { *b = append(*b, (*b)[0]) }},
		{"public IP", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { (*b)[0].Address = "8.8.8.8:8080" }},
		{"port", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) { (*b)[0].Address = "172.20.0.2:2019" }},
		{"admin path", func(m *domain.Manifest, d *Definition, b *[]Member, o *Options) {
			o.AdminSocket = "/run/oap-ingress/../docker.sock"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			m, d, b, o, now := fixture()
			test.change(&m, &d, &b, &o)
			if _, err := Compile("app", m, d, b, o, now); err == nil {
				t.Fatal("accepted unsafe input")
			}
		})
	}
}
