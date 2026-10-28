package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// journey is ~/.config/oap/journey.json: each server's place in onboarding.
type journey struct {
	Version int           `json:"version"`
	Servers []host.Server `json:"servers"`
}

func (j *journey) first() *host.Server {
	if len(j.Servers) == 0 {
		return nil
	}
	return &j.Servers[0]
}

// dir is OAP's directory on this machine: $OAP_CONFIG_DIR, else
// $XDG_CONFIG_HOME/oap, else ~/.config/oap.
func (a *app) dir() string {
	switch {
	case a.configDir != "":
		return a.configDir
	case os.Getenv("OAP_CONFIG_DIR") != "":
		return os.Getenv("OAP_CONFIG_DIR")
	case os.Getenv("XDG_CONFIG_HOME") != "":
		return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "oap")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "oap")
}

func (a *app) loadJourney() (*journey, error) {
	b, err := os.ReadFile(filepath.Join(a.dir(), "journey.json"))
	if errors.Is(err, os.ErrNotExist) {
		return &journey{Version: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var j journey
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("the journey file %s is damaged: %v", filepath.Join(a.dir(), "journey.json"), err)
	}
	return &j, nil
}

// update records a server's new place in the journey. The file is
// replaced atomically, so an interrupted run keeps the last completed step.
func (a *app) update(s host.Server) error {
	j, err := a.loadJourney()
	if err != nil {
		return err
	}
	replaced := false
	for i := range j.Servers {
		if j.Servers[i].Address == s.Address {
			j.Servers[i], replaced = s, true
		}
	}
	if !replaced {
		j.Servers = append(j.Servers, s)
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.dir(), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.dir(), ".journey-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(a.dir(), "journey.json"))
}

// stepIndex is the step a status is at, for the progress line.
func stepIndex(st host.Status) int {
	switch st.State {
	case host.StageNew:
		return 0
	case host.StageConnected:
		return 1
	case host.StageChecked, host.StageReady:
		for i, s := range host.Steps {
			if s == st.Next.Step {
				return i
			}
		}
		return 1
	case host.StageInstalled:
		return 4
	case host.StageOwned:
		return 5
	}
	return len(host.Steps)
}

var stepLabels = map[string]string{"owner": "owner key", "hand-off": "hand off"}

func renderSteps(w io.Writer, st host.Status) {
	at := stepIndex(st)
	var parts []string
	for i, s := range host.Steps {
		label := s
		if l, ok := stepLabels[s]; ok {
			label = l
		}
		mark := "·"
		switch {
		case i < at:
			mark = "✓"
		case i == at:
			mark = "→"
		}
		parts = append(parts, mark+" "+label)
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(parts, "   "))
}

var marks = map[host.Severity]string{host.Stop: "✗", host.NeedsFix: "!", host.Info: "·", host.Advice: "~"}

func renderStatus(w io.Writer, st host.Status, cli string) {
	if st.Server != "" {
		fmt.Fprintf(w, "Server %s", st.Server)
		if st.Summary != "" {
			fmt.Fprintf(w, ": %s", st.Summary)
		}
		fmt.Fprintln(w)
	}
	renderSteps(w, st)
	if len(st.Findings) > 0 {
		fmt.Fprintln(w)
	}
	for _, f := range st.Findings {
		line := upperFirst(f.Observed)
		if f.Required != "" && (f.Severity == host.Stop || f.Severity == host.NeedsFix) {
			line += ": needs " + f.Required
		}
		fmt.Fprintf(w, "%s %s.\n  %s\n", marks[f.Severity], line, f.Remedy)
		if f.Fix != nil {
			fmt.Fprintf(w, "  Fix (%s): %s.\n", f.Fix.Tier, f.Fix.Summary)
		}
	}
	fmt.Fprintln(w)
	n := st.Next
	switch {
	case st.Exit == host.ExitStopped:
		fmt.Fprintf(w, "Stopped: the server needs a change outside OAP (marked ✗ above).\nRun `%s` again afterwards.\n", cli)
	case n.Task != "":
		fmt.Fprintf(w, "Next: %s, by the %s. %s\nThis step isn't built yet (task %s).\n", n.Step, n.By, n.Why, n.Task)
	case n.Step == "done":
		fmt.Fprintln(w, "Onboarding is complete.")
	case n.By == host.ByOwner:
		fmt.Fprintf(w, "Next: %s, by you. %s\nRun `%s` to continue.\n", n.Step, n.Why, cli)
	case n.Command != "":
		fmt.Fprintf(w, "Next: %s. Run `%s`.\n", n.Step, n.Command)
	default:
		fmt.Fprintf(w, "Next: %s, by the %s. %s\n", n.Step, n.By, n.Why)
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
