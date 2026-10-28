package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

// The palette uses the terminal's own 16 colors, so it follows the owner's
// light or dark theme.
var (
	accent  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	good    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	caution = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	bad     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	faint   = lipgloss.NewStyle().Faint(true)
	bold    = lipgloss.NewStyle().Bold(true)
	panel   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
)

// tui draws the journey with Charm's Lip Gloss and Bubble Tea.
type tui struct {
	a     *app
	width int
}

func newTUI(a *app) *tui {
	w := 80
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 0 {
		w = c
	}
	return &tui{a: a, width: min(max(w, 60), 100)}
}

func (u *tui) print(s string) { lipgloss.Fprintln(u.a.out, s) }

func (u *tui) Welcome() { u.print(welcomeView(u.width)) }

func (u *tui) Status(st host.Status) { u.print(statusView(st, u.a.cli, u.width)) }

func (u *tui) Note(text string)    { u.print(text) }
func (u *tui) Success(text string) { u.print(good.Render("✓ ") + text) }
func (u *tui) Warn(text string)    { u.print(caution.Render("! ") + text) }

func (u *tui) Change(title, because, commands string) {
	u.print(changeView(title, because, commands, u.width))
}

// welcomeView is the first screen a new owner sees.
func welcomeView(width int) string {
	body := strings.Join([]string{
		accent.Render("OAP") + "  runs your apps on your own server, operated by your AI agent.",
		"",
		"No server is connected yet. Setup takes about 10 minutes and needs the",
		"server's " + bold.Render("IP address") + ", its " + bold.Render("username") + " and its " + bold.Render("password") + ".",
		"",
		stepTrack(host.Status{State: host.StageNew}),
	}, "\n")
	return panel.Width(width).Render(body)
}

// hang wraps text to width with every line after the first indented under
// the text, not under the prefix.
func hang(prefix, text string, width int) string {
	w := width - lipgloss.Width(prefix)
	return lipgloss.JoinHorizontal(lipgloss.Top, prefix, lipgloss.NewStyle().Width(max(w, 20)).Render(text))
}

// stepTrack is the journey on one line: done, current, still to come.
func stepTrack(st host.Status) string {
	at := stepIndex(st)
	var parts []string
	for i, s := range host.Steps {
		label := s
		if s == "hand-off" {
			label = "hand off"
		}
		switch {
		case i < at:
			parts = append(parts, good.Render("✓ "+label))
		case i == at && st.Exit == host.ExitStopped:
			parts = append(parts, bad.Render("✗ "+label))
		case i == at:
			parts = append(parts, accent.Render("● "+label))
		default:
			parts = append(parts, faint.Render("○ "+label))
		}
	}
	return strings.Join(parts, "   ")
}

// statusView is the journey panel: server, steps, what blocks it, next.
func statusView(st host.Status, cli string, width int) string {
	inner := width - 4 // border and padding
	var lines []string
	head := accent.Render("OAP")
	if st.Server != "" {
		head += "  " + bold.Render(st.Server)
	}
	lines = append(lines, head)
	if st.Summary != "" {
		lines = append(lines, faint.Render(strings.ReplaceAll(st.Summary, ", ", " · ")))
	}
	lines = append(lines, "", stepTrack(st))

	var notes []string
	for _, f := range st.Findings {
		obs := upperFirst(f.Observed)
		switch f.Severity {
		case host.Stop:
			lines = append(lines, "", hang(bad.Render("✗ "), bold.Render(obs), inner),
				hang("  ", faint.Render("needs "+f.Required), inner), hang("  ", f.Remedy, inner))
		case host.NeedsFix:
			text := bold.Render(obs)
			if f.Fix != nil {
				text += "  " + tierBadge(f.Fix.Tier)
			}
			lines = append(lines, "", hang(caution.Render("! "), text, inner), hang("  ", f.Remedy, inner))
		default:
			notes = append(notes, hang(faint.Render("· "), faint.Render(obs), inner))
		}
	}
	if len(notes) > 0 {
		lines = append(lines, "")
		lines = append(lines, notes...)
	}

	n := st.Next
	lines = append(lines, "")
	switch {
	case st.Exit == host.ExitStopped:
		lines = append(lines, bad.Render("Stopped.")+" Make the change marked ✗, then run "+bold.Render(cli)+" again.")
	case n.Step == "done":
		lines = append(lines, good.Render("Onboarding is complete."))
	case n.Task != "":
		lines = append(lines, accent.Render("Next ")+bold.Render(n.Step)+faint.Render(fmt.Sprintf(" · by the %s · not built yet (%s)", n.By, n.Task)))
	case n.By == host.ByOwner:
		lines = append(lines, accent.Render("Next ")+bold.Render(n.Step)+" · by you. Run "+bold.Render(cli)+" to continue.")
	case n.Command != "":
		lines = append(lines, accent.Render("Next ")+bold.Render(n.Step)+" · run "+bold.Render(n.Command))
	}
	return panel.Width(width).Render(strings.Join(lines, "\n"))
}

func tierBadge(t host.Tier) string {
	switch t {
	case host.Routine:
		return good.Render("[routine]")
	case host.Sensitive:
		return caution.Render("[sensitive]")
	}
	return bad.Render("[destructive]")
}

// changeView shows a change, and the exact commands it runs, before the
// owner approves it.
func changeView(title, because, commands string, width int) string {
	inner := width - 4
	lines := []string{hang("", bold.Render(title), inner), hang("", faint.Render("because "+because), inner), ""}
	for _, c := range strings.Split(strings.TrimRight(commands, "\n"), "\n") {
		lines = append(lines, hang(faint.Render("  │ "), faint.Render(c), inner))
	}
	return panel.Width(width).Render(strings.Join(lines, "\n"))
}

// Ask reads a line with a text input.
func (u *tui) Ask(label, def string) string {
	ti := textinput.New()
	ti.Placeholder = def
	ti.Prompt = ""
	ti.SetWidth(40)
	m := &askModel{label: label, def: def, input: ti}
	if _, err := tea.NewProgram(m, tea.WithOutput(u.a.out)).Run(); err != nil || m.cancelled {
		return ""
	}
	return m.value()
}

type askModel struct {
	label, def string
	input      textinput.Model
	done       bool
	cancelled  bool
}

func (m *askModel) Init() tea.Cmd { return m.input.Focus() }

func (m *askModel) value() string {
	if v := strings.TrimSpace(m.input.Value()); v != "" {
		return v
	}
	return m.def
}

func (m *askModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case k.Code == tea.KeyEnter:
			m.done = true
			return m, tea.Quit
		case k.Code == tea.KeyEscape || (k.Mod == tea.ModCtrl && k.Code == 'c'):
			m.cancelled, m.done = true, true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *askModel) View() tea.View {
	if m.done {
		if m.cancelled {
			return tea.NewView(faint.Render(m.label + "  cancelled\n"))
		}
		return tea.NewView(good.Render("✓ ") + m.label + "  " + bold.Render(m.value()) + "\n")
	}
	return tea.NewView(accent.Render("? ") + bold.Render(m.label) + "  " + m.input.View() + "\n")
}

// Confirm asks yes or no with single keys; anything but y is no.
func (u *tui) Confirm(question string) bool {
	m := &confirmModel{question: question}
	if _, err := tea.NewProgram(m, tea.WithOutput(u.a.out)).Run(); err != nil {
		return false
	}
	return m.yes
}

type confirmModel struct {
	question  string
	yes, done bool
}

func (m *confirmModel) Init() tea.Cmd { return nil }

func (m *confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case k.Text == "y" || k.Text == "Y":
		m.yes, m.done = true, true
	case k.Text == "n" || k.Text == "N" || k.Code == tea.KeyEnter || k.Code == tea.KeyEscape || (k.Mod == tea.ModCtrl && k.Code == 'c'):
		m.done = true
	default:
		return m, nil
	}
	return m, tea.Quit
}

func (m *confirmModel) View() tea.View {
	q := strings.Split(m.question, "\n")
	last := q[len(q)-1]
	pre := ""
	if len(q) > 1 {
		pre = strings.Join(q[:len(q)-1], "\n") + "\n"
	}
	switch {
	case !m.done:
		return tea.NewView(pre + accent.Render("? ") + bold.Render(last) + faint.Render("  y / N") + "\n")
	case m.yes:
		return tea.NewView(pre + good.Render("✓ ") + last + "  " + bold.Render("yes") + "\n")
	}
	return tea.NewView(pre + faint.Render("✗ "+last+"  no") + "\n")
}

// Run shows a spinner, the time taken and the last lines of output while
// work runs, then one line saying how it went.
func (u *tui) Run(label string, work func(progress func(line string)) error) error {
	m := &runModel{label: label, spin: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(accent)), start: time.Now(), width: u.width}
	p := tea.NewProgram(m, tea.WithOutput(u.a.out))
	m.work = func() tea.Msg {
		return runDone{err: work(func(l string) { p.Send(runLine(l)) })}
	}
	if _, err := p.Run(); err != nil {
		return err
	}
	return m.err
}

type (
	runLine string
	runDone struct{ err error }
	runTick struct{}
)

type runModel struct {
	label string
	spin  spinner.Model
	start time.Time
	took  time.Duration
	tail  []string
	work  tea.Cmd
	done  bool
	err   error
	width int
}

func (m *runModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.work, tea.Tick(time.Second, func(time.Time) tea.Msg { return runTick{} }))
}

func (m *runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case runLine:
		if l := strings.TrimSpace(string(msg)); l != "" {
			m.tail = append(m.tail, l)
			if len(m.tail) > 3 {
				m.tail = m.tail[len(m.tail)-3:]
			}
		}
	case runDone:
		m.done, m.err, m.took = true, msg.err, time.Since(m.start)
		return m, tea.Quit
	case runTick:
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return runTick{} })
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *runModel) View() tea.View {
	if m.done {
		took := faint.Render(fmt.Sprintf(" (%s)", m.took.Round(time.Second)))
		if m.err != nil {
			return tea.NewView(bad.Render("✗ ") + m.label + took + "\n")
		}
		return tea.NewView(good.Render("✓ ") + m.label + took + "\n")
	}
	s := m.spin.View() + " " + m.label + faint.Render(fmt.Sprintf("  %s", time.Since(m.start).Round(time.Second))) + "\n"
	for _, l := range m.tail {
		if len(l) > m.width-6 {
			l = l[:m.width-7] + "…"
		}
		s += faint.Render("  │ "+l) + "\n"
	}
	return tea.NewView(s)
}
