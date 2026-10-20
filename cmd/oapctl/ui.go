package main

import (
	"fmt"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// ui is how oapctl talks to the owner at a terminal. The plain version
// prints line by line and is what tests pin; the Charm version (tui.go)
// draws the same content as panels, prompts and live progress. Agents get
// neither: they get JSON.
type ui interface {
	// Welcome introduces OAP on the very first launch.
	Welcome()
	// Status shows where a server's journey stands.
	Status(st host.Status)
	// Ask reads a line, returning def when the answer is empty.
	Ask(label, def string) string
	// Confirm asks a yes-or-no question; the default is no.
	Confirm(question string) bool
	// Note, Success and Warn print one line.
	Note(text string)
	Success(text string)
	Warn(text string)
	// Change describes a change to the server before it is approved.
	Change(title, because string, commands string)
	// Run does slow work under a label, showing its output as it arrives.
	Run(label string, work func(progress func(line string)) error) error
}

// quietUI prints nothing: --json and --events keep stdout for JSON.
type quietUI struct{}

func (quietUI) Welcome()                                          {}
func (quietUI) Status(host.Status)                                {}
func (quietUI) Ask(label, def string) string                      { return def }
func (quietUI) Confirm(string) bool                               { return false }
func (quietUI) Note(string)                                       {}
func (quietUI) Success(string)                                    {}
func (quietUI) Warn(string)                                       {}
func (quietUI) Change(string, string, string)                     {}
func (quietUI) Run(_ string, work func(func(string)) error) error { return work(func(string) {}) }

// plainUI writes text, one line at a time.
type plainUI struct {
	a   *app
	cli string
}

func (u plainUI) Welcome() {
	fmt.Fprintf(u.a.out, "OAP runs your apps on your own server, operated by your AI agent.\n"+
		"You don't have a server connected yet. Setup takes about 10 minutes and needs:\n"+
		"the server's IP address, its username and its password.\n\n")
	renderSteps(u.a.out, host.Status{State: host.StageNew})
	fmt.Fprintln(u.a.out)
}

func (u plainUI) Status(st host.Status) { renderStatus(u.a.out, st, u.cli) }

func (u plainUI) Ask(label, def string) string {
	if def != "" {
		return u.a.readLine(fmt.Sprintf("%s [%s]: ", label, def), def)
	}
	return u.a.readLine(label+": ", "")
}

func (u plainUI) Confirm(question string) bool {
	ans := u.a.readLine(question+" [y/N] ", "n")
	return strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")
}

func (u plainUI) Note(text string)    { fmt.Fprintln(u.a.out, text) }
func (u plainUI) Success(text string) { fmt.Fprintln(u.a.out, "✓ "+text) }
func (u plainUI) Warn(text string)    { fmt.Fprintln(u.a.out, text) }

func (u plainUI) Change(title, because, commands string) {
	fmt.Fprintf(u.a.out, "%s\n  Because: %s.\n  Commands:\n%s\n", title, because, indent(commands, "    "))
}

func (u plainUI) Run(label string, work func(progress func(line string)) error) error {
	fmt.Fprintln(u.a.out, label+"…")
	return work(func(string) {})
}

// readLine prints a prompt and reads one line from stdin.
func (a *app) readLine(prompt, def string) string {
	fmt.Fprint(a.out, prompt)
	line, _ := a.in.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}
