package ingress

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmptySharedServerRetainsOnlyDenyListenerAndPrivateAdmin(t *testing.T) {
	_, _, _, options, now := fixture()
	options.InternalCA = true
	candidate, err := CompileServer(nil, options, now)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(candidate.JSON(), &config); err != nil {
		t.Fatal(err)
	}
	apps := config["apps"].(map[string]any)
	if _, ok := apps["tls"]; ok || strings.Contains(string(candidate.JSON()), "reverse_proxy") || strings.Contains(string(candidate.JSON()), "upstreams") {
		t.Fatal("withdrawn server retains routes or certificate automation")
	}
	if config["admin"].(map[string]any)["listen"] != "unix/"+options.AdminSocket+"|0600" || !strings.Contains(string(candidate.JSON()), `"status_code":404`) {
		t.Fatal("empty server lacks private admin or deny response")
	}
	options.AdminSocket = "/tmp/foreign.sock"
	if _, err = CompileServer(nil, options, now); err == nil {
		t.Fatal("empty server bypassed authority validation")
	}
}

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
