package host

import (
	"fmt"
	"strings"
)

// Stage is how far a server is through onboarding
// (docs/architecture/onboarding.md, "First launch").
type Stage string

const (
	StageNew       Stage = "new"
	StageConnected Stage = "connected"
	StageChecked   Stage = "checked"
	StageReady     Stage = "ready"
	StageInstalled Stage = "installed"
	StageOwned     Stage = "owned"
	StageHandedOff Stage = "handed-off"
)

// Steps names the journey's steps in order.
var Steps = []string{"connect", "check", "fix", "install", "owner", "hand-off"}

// Server is one server's place in the journey, as stored on the laptop.
type Server struct {
	Address  string    `json:"address"` // user@host[:port]
	Stage    Stage     `json:"stage"`
	Summary  string    `json:"summary,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
}

// Who does a step.
const (
	ByOwner = "owner"
	ByAgent = "agent"
)

// Exit codes for `oap` with no arguments.
const (
	ExitOK          = 0 // done, or the next step is the agent's to run
	ExitOwner       = 3 // waiting for the owner
	ExitStopped     = 4 // something outside OAP must change first
	ExitNotBuiltYet = 5 // the next step isn't built yet; Task names it
)

// Next is the journey's next step.
type Next struct {
	Step string `json:"step"`
	By   string `json:"by"`
	Why  string `json:"why"`
	// AskOwner is what an agent should tell the owner, for owner steps.
	AskOwner string `json:"ask_owner,omitempty"`
	// Command is what runs the step, for agent steps.
	Command string `json:"command,omitempty"`
	// Task names the plan task that delivers a step not built yet.
	Task string `json:"task,omitempty"`
	// Then is what to run after the step.
	Then string `json:"then,omitempty"`
}

// Status is what `oap` reports with no arguments.
type Status struct {
	State    Stage     `json:"state"`
	Server   string    `json:"server,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Next     Next      `json:"next"`
	Journey  []string  `json:"journey"`
	Findings []Finding `json:"findings,omitempty"`
	Exit     int       `json:"-"`
}

// Plan works out the next step for a server, or for a first server when
// there is none. cli is the command's name as the caller invokes it.
func Plan(s *Server, cli string) Status {
	st := Status{State: StageNew, Journey: Steps}
	then := cli + " --json"
	if s == nil {
		st.Next = Next{Step: "connect", By: ByOwner,
			Why:      "Connecting needs the server password, which agents must not handle.",
			AskOwner: fmt.Sprintf("Please run `%s` in a terminal on this machine and follow the steps. Tell me when it says Connected.", cli),
			Then:     then}
		st.Exit = ExitOwner
		return st
	}
	st.State, st.Server, st.Summary = s.Stage, s.Address, s.Summary
	switch s.Stage {
	case StageNew:
		st.Next = Next{Step: "connect", By: ByOwner,
			Why:      "Connecting needs the server password, which agents must not handle.",
			AskOwner: fmt.Sprintf("Please run `%s host connect %s` in a terminal on this machine. Tell me when it says Connected.", cli, s.Address),
			Then:     then}
		st.Exit = ExitOwner
	case StageConnected:
		st.Next = Next{Step: "check", By: ByAgent, Why: "The check only reads the server, so an agent may run it.",
			Command: fmt.Sprintf("%s host check %s", cli, s.Address), Then: then}
		st.Exit = ExitOK
	case StageChecked, StageReady:
		st.Findings = s.Findings
		blocked, stopped := Blocking(s.Findings)
		switch {
		case stopped:
			var asks []string
			for _, f := range s.Findings {
				if f.Severity == Stop {
					asks = append(asks, f.Remedy)
				}
			}
			st.Next = Next{Step: "check", By: ByOwner,
				Why:      "The server can't run OAP until something outside OAP changes.",
				AskOwner: strings.Join(asks, " "),
				Command:  fmt.Sprintf("%s host check %s", cli, s.Address), Then: then}
			st.Exit = ExitStopped
		case blocked:
			st.Next = Next{Step: "fix", By: ByOwner,
				Why:      "Fixes change the server, so the owner approves each one.",
				AskOwner: fmt.Sprintf("Please run `%s host fix` in a terminal on this machine and approve the fixes it shows. Tell me when it's done.", cli),
				Command:  fmt.Sprintf("%s host fix %s --json", cli, s.Address), Then: then}
			st.Exit = ExitOwner
		default:
			st.State = StageReady
			st.Next = Next{Step: "install", By: ByAgent, Why: "The server meets every requirement.", Task: "R3.13", Then: then}
			st.Exit = ExitNotBuiltYet
		}
	case StageInstalled:
		st.Next = Next{Step: "owner", By: ByOwner, Why: "Only the owner creates the owner account.", Task: "R3.6", Then: then}
		st.Exit = ExitNotBuiltYet
	case StageOwned:
		st.Next = Next{Step: "hand-off", By: ByAgent, Why: "The agent signs in with a device login and the bootstrap key is removed.", Task: "R3.14", Then: then}
		st.Exit = ExitNotBuiltYet
	case StageHandedOff:
		st.Next = Next{Step: "done", By: ByAgent, Why: "Onboarding is complete; the agent works through OAP from here."}
		st.Exit = ExitOK
	default:
		st.Next = Next{Step: "connect", By: ByOwner, Why: fmt.Sprintf("Unknown stage %q in the journey file.", s.Stage),
			AskOwner: fmt.Sprintf("Please run `%s host connect %s` again in a terminal.", cli, s.Address), Then: then}
		st.Exit = ExitOwner
	}
	return st
}
