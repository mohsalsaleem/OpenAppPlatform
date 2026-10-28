package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;:]*[A-Za-z]`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// TestTUIViewsGolden pins the Charm views' text and layout, colors removed.
func TestTUIViewsGolden(t *testing.T) {
	stopped := host.Plan(&host.Server{Address: "root@139.180.132.23", Stage: host.StageChecked,
		Summary: "Ubuntu 18.04.2 LTS, x86_64, 1 CPU, 985 MiB memory, 12.1 GiB disk free", Findings: findings(t, "vultr-oap-2026-10-19.txt")}, "oapctl")
	fixes := host.Plan(&host.Server{Address: "root@139.180.132.23", Stage: host.StageChecked,
		Summary: "Ubuntu 26.04.1 LTS, x86_64, 1 CPU, 950 MiB memory, 15.7 GiB disk free", Findings: findings(t, "vultr-oap-ubuntu-26-04.txt")}, "oapctl")
	ready := host.Plan(&host.Server{Address: "root@139.180.132.23", Stage: host.StageReady,
		Summary: "Ubuntu 26.04.1 LTS, x86_64, 1 CPU, 950 MiB memory, 15.2 GiB disk free"}, "oapctl")
	var b strings.Builder
	for _, v := range []struct{ name, view string }{
		{"welcome", welcomeView(80)},
		{"stopped", statusView(stopped, "oapctl", 80)},
		{"fixes", statusView(fixes, "oapctl", 80)},
		{"ready", statusView(ready, "oapctl", 80)},
		{"change", changeView("Add a 1 GiB swap file (routine; undo with `oapctl host undo swap-file`)", "985 MiB memory, no swap", host.Scripts["swap-file"].Apply, 80)},
	} {
		b.WriteString("== " + v.name + "\n" + plain(v.view) + "\n")
	}
	golden(t, "tui-views.txt", b.String())
}

func key(text string, code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Text: text, Code: code} }

func TestConfirmDefaultsToNo(t *testing.T) {
	for _, c := range []struct {
		key  tea.KeyPressMsg
		want bool
	}{{key("y", 'y'), true}, {key("Y", 'Y'), true}, {key("n", 'n'), false}, {key("", tea.KeyEnter), false}, {key("", tea.KeyEscape), false}} {
		m := &confirmModel{question: "Apply this?"}
		m.Update(key("x", 'x')) // ignored
		if m.done {
			t.Fatal("an unrelated key answered")
		}
		_, cmd := m.Update(c.key)
		if m.yes != c.want || cmd == nil {
			t.Fatalf("%+v: yes=%v", c.key, m.yes)
		}
	}
}

func TestRunModelShowsTailAndOutcome(t *testing.T) {
	m := &runModel{label: "Applying install-docker", width: 80}
	for _, l := range []string{"Get:1 a", "", "Get:2 b", "Unpacking c", "Setting up d"} {
		m.Update(runLine(l))
	}
	if v := plain(m.View().Content); !strings.Contains(v, "│ Get:2 b") || strings.Contains(v, "Get:1 a") || !strings.Contains(v, "Setting up d") {
		t.Fatalf("tail:\n%s", v)
	}
	m.Update(runDone{err: errors.New("boom")})
	if v := plain(m.View().Content); !strings.HasPrefix(v, "✗ Applying install-docker") || m.err == nil {
		t.Fatalf("outcome %q", v)
	}
}

func TestAskUsesDefaultAndCancels(t *testing.T) {
	m := &askModel{label: "Username", def: "root"}
	m.Update(key("", tea.KeyEnter))
	if !m.done || m.value() != "root" || !strings.Contains(plain(m.View().Content), "Username  root") {
		t.Fatalf("default: %+v %q", m, plain(m.View().Content))
	}
	m = &askModel{label: "Server address"}
	m.Update(key("", tea.KeyEscape))
	if !m.cancelled || !strings.Contains(plain(m.View().Content), "cancelled") {
		t.Fatal("escape must cancel")
	}
}
