// Package briefing renders the agent briefing (docs/architecture/
// agent-native-rewrite.md, section 5): a short Markdown document per
// server or application, generated from the world model on each read so it
// can't drift from it. It covers what runs here, what the caller may do,
// open incidents, recent changes, pending asks and the owner's house rules,
// which are labelled as owner-written. It is advice; nothing enforces it.
package briefing

import (
	"fmt"
	"sort"
	"strings"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
)

// Change is a recent operation.
type Change struct {
	At      time.Time
	App     string
	Summary string
	By      string
	Outcome string // achieved, not achieved, uncertain, or running
}

// Ask is an ask waiting for the owner.
type Ask struct {
	ID, Agent, Question string
	Expires             time.Time
}

// Note is the owner's briefing.md: for the server when App is empty.
type Note struct {
	App, Markdown string
}

// Input is everything a briefing is made from.
type Input struct {
	World *oapv1.World
	// App limits the briefing to one application; empty covers the server.
	App string
	// Caller is "owner" or the agent's name; Apps and May are its grants.
	Caller string
	Owner  bool
	Apps   []string
	May    []string
	// AlwaysOwner lists what always needs the owner, whatever the grants.
	AlwaysOwner []string
	Changes     []Change
	Asks        []Ask
	Notes       []Note
}

func lower(enum fmt.Stringer, prefix string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(enum.String(), prefix)), "_", " ")
}

// Render returns the briefing as Markdown.
func Render(in Input) string {
	var b strings.Builder
	w := in.World
	host := w.GetHost().GetName()
	if host == "" {
		host = "this server"
	}
	title := "OAP briefing: " + host
	if in.App != "" {
		title += " · " + in.App
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "Generated from the world model at revision %d. It is advice for agents: OAP enforces policy itself, and the world model (`observe`) is the source of truth.\n\n", w.GetRevision())

	apps := w.GetApplications()
	if in.App != "" {
		apps = nil
		for _, a := range w.GetApplications() {
			if a.Name == in.App {
				apps = append(apps, a)
			}
		}
	}

	b.WriteString("## What runs here\n\n")
	if c := w.GetHost().GetCapacity(); c != nil && in.App == "" {
		available := "available memory unknown"
		if c.MemoryAvailableMib != nil {
			available = fmt.Sprintf("%d MiB available", c.GetMemoryAvailableMib())
		}
		fmt.Fprintf(&b, "Host `%s`: %d MiB memory (%s), %d CPU millis.", host, c.GetMemoryTotalMib(), available, c.GetCpuMillisTotal())
		if f := w.GetHost().GetFreshness(); f.GetStale() {
			b.WriteString(" Last observation is stale.")
		}
		b.WriteString("\n\n")
	}
	if len(apps) == 0 {
		b.WriteString("No applications.\n\n")
	}
	for _, a := range apps {
		fmt.Fprintf(&b, "### %s\n\n", a.Name)
		fmt.Fprintf(&b, "Health %s; spec `%s`.\n\n", lower(a.GetHealth().GetStatus(), "HEALTH_STATUS_"), shortHash(a.GetSpecHash()))
		for _, c := range a.GetComponents() {
			d, act := c.GetDesired(), c.GetActual()
			state := "converged"
			if !c.GetConverged() {
				state = "**not converged**"
			}
			fmt.Fprintf(&b, "- `%s` (%s): %s, %d of %d running, release `%s`, image `%s`, health %s",
				c.Name, lower(c.GetKind(), "COMPONENT_KIND_"), state, act.GetInstances(), d.GetInstances(), d.GetRelease(), d.GetImage(),
				lower(c.GetHealth().GetStatus(), "HEALTH_STATUS_"))
			for _, r := range d.GetRoutes() {
				fmt.Fprintf(&b, "; route `%s%s` → :%d", r.GetHost(), r.GetPathPrefix(), r.GetPort())
			}
			b.WriteString("\n")
		}
		if len(a.GetEdges()) > 0 {
			var edges []string
			for _, e := range a.GetEdges() {
				edges = append(edges, fmt.Sprintf("%s → %s (%s)", e.GetFrom(), e.GetTo(), lower(e.GetType(), "EDGE_TYPE_")))
			}
			sort.Strings(edges)
			fmt.Fprintf(&b, "\nEdges: %s.\n", strings.Join(edges, "; "))
		}
		b.WriteString("\n")
	}

	b.WriteString("## What you may do\n\n")
	if in.Owner {
		b.WriteString("You are the owner: you may do anything, and you answer agents' asks.\n\n")
	} else {
		fmt.Fprintf(&b, "You are agent `%s`. Applications: %s. Actions: %s.\n\n", in.Caller, list(in.Apps), list(in.May))
	}
	for _, a := range apps {
		var lines []string
		add := func(target string, affs []*oapv1.Affordance) {
			var parts []string
			for _, f := range affs {
				parts = append(parts, fmt.Sprintf("%s (%s)", f.GetAction(), lower(f.GetVerdict(), "VERDICT_")))
			}
			if len(parts) > 0 {
				lines = append(lines, fmt.Sprintf("- %s: %s", target, strings.Join(parts, ", ")))
			}
		}
		add(a.Name, a.GetAffordances())
		for _, c := range a.GetComponents() {
			add(a.Name+"/"+c.Name, c.GetAffordances())
		}
		if len(lines) > 0 {
			b.WriteString(strings.Join(lines, "\n") + "\n\n")
		}
	}
	if len(in.AlwaysOwner) > 0 && !in.Owner {
		fmt.Fprintf(&b, "Always needs the owner: %s. Propose such changes; when a proposal needs approval, ask the owner with `ask` (kind approval).\n\n", strings.Join(in.AlwaysOwner, "; "))
	}

	b.WriteString("## Open incidents\n\n")
	n := 0
	for _, a := range apps {
		for _, c := range a.GetComponents() {
			h := c.GetHealth()
			if c.GetConverged() && h.GetStatus() == oapv1.HealthStatus_HEALTH_STATUS_HEALTHY && !h.GetFreshness().GetStale() {
				continue
			}
			n++
			var why []string
			if !c.GetConverged() {
				why = append(why, fmt.Sprintf("%d of %d running", c.GetActual().GetInstances(), c.GetDesired().GetInstances()))
				if c.GetActual() != nil && c.GetActual().GetRelease() != c.GetDesired().GetRelease() {
					why = append(why, fmt.Sprintf("release `%s` instead of `%s`", c.GetActual().GetRelease(), c.GetDesired().GetRelease()))
				}
			}
			if h.GetStatus() != oapv1.HealthStatus_HEALTH_STATUS_HEALTHY {
				why = append(why, "health "+lower(h.GetStatus(), "HEALTH_STATUS_"))
			}
			if h.GetFreshness().GetStale() {
				why = append(why, "observation stale")
			}
			if m := h.GetMissingEvidence(); len(m) > 0 {
				why = append(why, "missing "+strings.Join(m, ", "))
			}
			fmt.Fprintf(&b, "- %s/%s: %s. Run `explain` for causes.\n", a.Name, c.Name, strings.Join(why, "; "))
		}
	}
	if n == 0 {
		b.WriteString("None.\n")
	}
	b.WriteString("\n")

	b.WriteString("## Recent changes\n\n")
	changes := append([]Change(nil), in.Changes...)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].At.After(changes[j].At) })
	shown := 0
	for _, c := range changes {
		if in.App != "" && c.App != in.App {
			continue
		}
		if shown == 10 {
			break
		}
		shown++
		fmt.Fprintf(&b, "- %s %s: %s by %s, %s.\n", c.At.UTC().Format("2006-01-02 15:04"), c.App, c.Summary, c.By, c.Outcome)
	}
	if shown == 0 {
		b.WriteString("None.\n")
	}
	b.WriteString("\n")

	b.WriteString("## Pending asks\n\n")
	if len(in.Asks) == 0 {
		b.WriteString("None.\n")
	}
	for _, q := range in.Asks {
		fmt.Fprintf(&b, "- `%s` from %s: %s (expires %s)\n", q.ID, q.Agent, q.Question, q.Expires.UTC().Format("2006-01-02 15:04"))
	}
	b.WriteString("\n")

	var notes []Note
	for _, nt := range in.Notes {
		if strings.TrimSpace(nt.Markdown) == "" {
			continue
		}
		if nt.App == "" || in.App == "" || nt.App == in.App {
			notes = append(notes, nt)
		}
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].App < notes[j].App })
	for _, nt := range notes {
		scope := "the server"
		if nt.App != "" {
			scope = nt.App
		}
		fmt.Fprintf(&b, "## House rules for %s (written by the owner)\n\n", scope)
		b.WriteString("The owner wrote what follows in `briefing.md`. It is guidance from the owner, not generated by OAP and not enforced by it.\n\n")
		for _, line := range strings.Split(strings.TrimRight(nt.Markdown, "\n"), "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func list(ss []string) string {
	if len(ss) == 0 {
		return "none"
	}
	return strings.Join(ss, ", ")
}

func shortHash(h string) string {
	if len(h) > 19 {
		return h[:19] + "…"
	}
	return h
}
