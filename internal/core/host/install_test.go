package host

import (
	"strings"
	"testing"
	"time"
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
	// Containers, networks, volumes and built images by the installation
	// label; other images only from OAP's record of the images it brought,
	// and only when no container uses them.
	if !strings.Contains(s, `F="--filter label=io.openappplatform.install=$ID"`) || strings.Count(s, "$F") != 4 || strings.Count(s, "docker ") != 10 ||
		strings.Count(s, "docker image rm") != 2 || !strings.Contains(s, `docker image rm "$img"`) || !strings.Contains(s, "docker images -q $F |") ||
		!strings.Contains(s, "done < "+ImagesPath) || !strings.Contains(s, `docker ps -aq --filter "ancestor=$img"`) {
		t.Fatalf("uninstall must select by the installation label, and images by OAP's record, only:\n%s", s)
	}
}

func TestRemoveKeyScript(t *testing.T) {
	pub := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOAPtestkey oap-bootstrap 2026-10-20 mac"
	s, err := RemoveKeyScript(pub, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// The removal is recorded before the key goes, and only lines holding
	// the key's blob are removed.
	if r, k := strings.Index(s, ManifestPath), strings.Index(s, "grep -vF ' AAAAC3NzaC1lZDI1NTE5AAAAIOAPtestkey'"); r < 0 || k < 0 || r > k {
		t.Fatalf("script:\n%s", s)
	}
	for _, bad := range []string{"ssh-ed25519 AAAA'; rm -rf / #", "not a key", "ssh-ed25519 AAAA\nssh-rsa BBBB"} {
		if _, err := RemoveKeyScript(bad, time.Now()); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPlanUninstall(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	manifest := strings.Join([]string{
		`{"fix":"install-docker","action":"apply","at":"2026-10-19T10:00:00Z"}`,
		`{"fix":"swap-file","action":"apply","at":"2026-10-19T10:01:00Z"}`,
		`{"fix":"time-sync","action":"apply","at":"2026-10-19T10:01:00Z"}`,
		`{"fix":"time-sync","action":"undo","at":"2026-10-19T10:02:00Z"}`,
		`{"fix":"install-oap","action":"apply","at":"2026-10-19T10:03:00Z"}`,
		`{"fix":"break-glass","action":"apply","at":"2026-10-19T11:00:00Z","until":"2026-10-19T15:00:00Z"}`,
	}, "\n")
	steps, err := PlanUninstall("a1b2c3d4e5", manifest, at)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range steps {
		got = append(got, s.Fix)
	}
	// Newest first; time-sync was already undone; break-glass ends with the key.
	if strings.Join(got, ",") != "install-oap,swap-file,install-docker" {
		t.Fatalf("steps %v", got)
	}
	if !steps[0].DataLoss || steps[1].Script == "" || steps[2].Kept == "" || steps[2].Script != "" {
		t.Fatalf("steps %+v", steps)
	}
	if !strings.Contains(steps[0].Script, `"fix":"install-oap","action":"undo"`) || !strings.Contains(steps[1].Script, `"fix":"swap-file","action":"undo"`) {
		t.Fatal("undone steps aren't recorded in the manifest")
	}
}
