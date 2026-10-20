package host

import (
	"strings"
	"testing"
)

func TestInstallScript(t *testing.T) {
	in := Install{ID: "a1b2c3d4e5", Oapd: "oapd:abc1234", Caddy: CaddyImage, TLS: true}
	s, err := in.Script()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"io.openappplatform.install=$ID", "io.openappplatform.role=system", "io.openappplatform.role=network",
		`--restart unless-stopped`, "-p 80:80 -p 443:443", OapdSocket, "OAPD_TLS=$TLS", "TLS=true",
		`echo "caddy unchanged"`, `echo "oapd unchanged"`, "grace_period", "--memory 160m",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	if strings.Contains(s, "docker rm -f $(") || strings.Contains(s, "prune") {
		t.Fatal("install script may remove resources it didn't create")
	}
	for _, bad := range []Install{{ID: "BAD", Oapd: "x", Caddy: "y"}, {ID: "abcdef1234", Oapd: "oapd; rm -rf /", Caddy: CaddyImage}} {
		if _, err := bad.Script(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestUninstallTouchesOnlyTheInstallation(t *testing.T) {
	s, err := Install{ID: "a1b2c3d4e5"}.Uninstall()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `F="--filter label=io.openappplatform.install=$ID"`) || strings.Count(s, "$F") != 3 || strings.Count(s, "docker ") != 6 {
		t.Fatalf("uninstall must select by the installation label only:\n%s", s)
	}
}
