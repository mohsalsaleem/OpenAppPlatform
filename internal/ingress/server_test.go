package ingress

import (
	"strings"
	"testing"
)

func TestSharedServerPreservesAppsModesAndDeterminism(t *testing.T) {
	m, d, members, o, now := fixture()
	m.Ingress = &d
	second := m
	other := d
	other.Domain = "other.example.test"
	other.HTTPS = true
	second.Ingress = &other
	peers := append([]Member(nil), members...)
	for i := range peers {
		peers[i].ApplicationID = "other"
		peers[i].Address = strings.Replace(peers[i].Address, "172.20.", "172.21.", 1)
	}
	o.InternalCA = true
	apps := []ApplicationRoutes{{ApplicationID: "app", Manifest: m, Members: members}, {ApplicationID: "other", Manifest: second, Members: peers}}
	candidate, err := CompileServer(apps, o, now)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(candidate.JSON())
	for _, term := range []string{`"oap-http"`, `"oap-https"`, `"app.example.test"`, `"other.example.test"`, `"module":"internal"`} {
		if !strings.Contains(raw, term) {
			t.Fatal("missing", term)
		}
	}
	apps[0], apps[1] = apps[1], apps[0]
	same, err := CompileServer(apps, o, now)
	if err != nil || same.Hash() != candidate.Hash() {
		t.Fatal("unstable shared configuration", err)
	}
	other.Domain = d.Domain
	if _, err = CompileServer(apps, o, now); err == nil {
		t.Fatal("duplicate domain accepted")
	}
	other.Domain = "other.example.test"
	apps[0].ApplicationID = "app"
	if _, err = CompileServer(apps, o, now); err == nil {
		t.Fatal("duplicate app accepted")
	}
}
