package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/proposal"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
)

// Extensions (R4.1). Installing, upgrading or removing an extension is a
// proposal like any other, judged by the envelope ("extensions" is a
// sensitive category by default) and applied as an operation whose steps
// are journaled, so await and watch follow it. Extensions are server-wide:
// only a caller whose scope is every application may propose them.

// WhyNoExtensionHost is the error when the server runs no extension host.
const WhyNoExtensionHost = "service.no_extension_host"

var extMu sync.Mutex // one extension operation at a time

func extensionOutOfScope(c Caller) error {
	return fail(PermissionDenied, WhyOutOfScope, "agent %s may not change the server's extensions; that needs a scope of every application", c.Agent)
}

// proposeExtension is Propose for extension intents. The caller holds s.mu.
func (s *Service) proposeExtension(ctx context.Context, c Caller, req *oapv1.ProposeRequest, head uint64) (*oapv1.ProposeResponse, error) {
	in := req.GetIntent()
	current, baseHash, err := s.Desired.Extensions(ctx)
	if err != nil {
		return nil, err
	}
	res, ierr := intent.CompileExtension(current, in)
	if ierr != nil {
		return nil, &Error{Code: InvalidArgument, Detail: ierr.Detail()}
	}
	env, err := s.envelope(ctx)
	if err != nil {
		return nil, err
	}
	out := &oapv1.Proposal{BaseRevision: head, BaseSpecHash: baseHash, Agent: c.Agent, Intent: in,
		Steps: extensionSteps(res), Undo: extensionUndo(res), EnvelopeVersion: env.GetVersion(), Context: &oapv1.PolicyContext{}}
	d := policy.Evaluate(policy.Input{Agent: c.Agent, Envelope: env, Proposal: out, Kind: string(res.Kind), Changes: res.Changes})
	out.Verdict, out.Risk, out.Reasons, out.Next = d.Verdict, d.Risk, d.Reasons, d.Next
	id, err := proposal.ID(out)
	if err != nil {
		return nil, err
	}
	out.Id = id
	if _, err := s.append(ctx, KindProposal, "", &oapv1.JournalEntry{Id: id, Kind: oapv1.EntryKind_ENTRY_KIND_PROPOSAL,
		Actor: actor(c), BaseRevision: head, Summary: summary(in), Detail: &oapv1.JournalEntry_Proposal{Proposal: out}}); err != nil {
		return nil, err
	}
	return &oapv1.ProposeResponse{Proposal: out}, nil
}

func extensionSteps(res *intent.ExtensionResult) []*oapv1.Step {
	write := &oapv1.Step{Kind: oapv1.StepKind_STEP_KIND_WRITE_DESIRED, Component: res.Name}
	if res.New == nil {
		return []*oapv1.Step{{Kind: oapv1.StepKind_STEP_KIND_STOP_EXTENSION, Component: res.Name}, write}
	}
	return []*oapv1.Step{write, {Kind: oapv1.StepKind_STEP_KIND_START_EXTENSION, Component: res.Name}}
}

// extensionUndo restores the extension as it was: removed if it is new,
// its earlier install otherwise.
func extensionUndo(res *intent.ExtensionResult) *oapv1.Intent {
	u := &oapv1.Intent{Because: "undo"}
	if res.Old == nil {
		u.Change = &oapv1.Intent_RemoveExtension{RemoveExtension: &oapv1.RemoveExtension{Name: res.Name}}
		return u
	}
	o := res.Old
	u.Change = &oapv1.Intent_InstallExtension{InstallExtension: &oapv1.InstallExtension{Name: o.Name, Path: o.Path, Sha256: o.SHA256,
		Points: o.Points, Settings: o.Settings, Secrets: o.Secrets, MemoryMib: int32(o.MemoryMiB)}}
	return u
}

// checkExtensionBase refuses to apply a proposal whose extensions changed
// since it was made.
func (s *Service) checkExtensionBase(ctx context.Context, p *oapv1.Proposal) error {
	_, h, err := s.Desired.Extensions(ctx)
	if err != nil {
		return err
	}
	if h != p.GetBaseSpecHash() {
		e := fail(FailedPrecondition, WhyStaleBase, "the server's extensions changed since this proposal was made; propose again")
		e.Detail.Remedy = refresh
		return e
	}
	return nil
}

// runExtension carries out an extension operation, journaling each step
// as the executor does. A start that fails puts the extensions back as
// they were.
func (s *Service) runExtension(id string, p *oapv1.Proposal) {
	ctx := context.WithoutCancel(s.runCtx)
	go func() {
		extMu.Lock()
		defer extMu.Unlock()
		s.outcome(id, s.runExtensionSteps(ctx, id, p))
	}()
}

func (s *Service) runExtensionSteps(ctx context.Context, id string, p *oapv1.Proposal) error {
	if s.Extensions == nil {
		return &engine.StepError{Step: 0, Kind: p.GetSteps()[0].GetKind(), Reason: "this server runs no extension host"}
	}
	before, _, err := s.Desired.Extensions(ctx)
	if err != nil {
		return err
	}
	res, ierr := intent.CompileExtension(before, p.GetIntent())
	if ierr != nil && ierr.Why != intent.WhyNoChange { // resumed after the desired state was written
		return &engine.StepError{Step: 0, Kind: p.GetSteps()[0].GetKind(), Reason: ierr.Message}
	}
	done := map[int]bool{}
	entries, _ := s.entries(ctx, 0)
	for _, e := range entries {
		if e.Kind == engine.KindStep && e.Operation == id && e.State == engine.StateSucceeded {
			done[e.Step] = true
		}
	}
	for i, step := range p.GetSteps() {
		if done[i] {
			continue
		}
		s.recordStep(ctx, id, i, engine.StateStarted, "")
		var err error
		switch step.GetKind() {
		case oapv1.StepKind_STEP_KIND_WRITE_DESIRED:
			if res != nil {
				_, err = s.Desired.SetExtensions(ctx, res.Tree, p.GetAgent(), summary(p.GetIntent()))
			}
		case oapv1.StepKind_STEP_KIND_START_EXTENSION:
			var e spec.Extension
			if res != nil && res.New != nil {
				e = *res.New
			} else {
				cur, _, _ := s.Desired.Extensions(ctx)
				all, _ := spec.ParseExtensions(cur)
				e = all[step.GetComponent()]
			}
			if err = s.Extensions.Install(ctx, e, true); err != nil {
				// It doesn't run as granted: put the extensions back.
				s.Extensions.Remove(e.Name)
				if res != nil && res.Old != nil {
					s.Extensions.Install(ctx, *res.Old, false)
				}
				s.Desired.SetExtensions(ctx, before, "oapd", "Restore extensions: "+e.Name+" didn't start")
			}
		case oapv1.StepKind_STEP_KIND_STOP_EXTENSION:
			s.Extensions.Remove(step.GetComponent())
		default:
			err = fmt.Errorf("not an extension step")
		}
		if err != nil {
			s.recordStep(ctx, id, i, engine.StateFailed, err.Error())
			return &engine.StepError{Step: i, Kind: step.GetKind(), Reason: err.Error()}
		}
		s.recordStep(ctx, id, i, engine.StateSucceeded, "")
	}
	return nil
}

func (s *Service) recordStep(ctx context.Context, id string, step int, state, reason string) {
	data, _ := json.Marshal(map[string]string{"reason": reason})
	if reason == "" {
		data = []byte("{}")
	}
	s.Journal.Append(ctx, ops.Entry{At: s.Clock.Now(), Kind: engine.KindStep, Operation: id, Step: step, State: state, Data: data})
}

// loadExtensions registers the installed extensions with the host; they
// start on first use.
func (s *Service) loadExtensions(ctx context.Context) error {
	if s.Extensions == nil {
		return nil
	}
	tree, _, err := s.Desired.Extensions(ctx)
	if err != nil {
		return err
	}
	all, probs := spec.ParseExtensions(tree)
	if len(probs) > 0 {
		return fmt.Errorf("extensions.yaml: %s at %s", probs[0].Message, probs[0].Path)
	}
	for _, e := range all {
		if err := s.Extensions.Install(ctx, e, false); err != nil {
			return err
		}
	}
	return nil
}

// isExtensionOp reports whether a proposal changes extensions.
func isExtensionOp(p *oapv1.Proposal) bool { return intent.IsExtension(p.GetIntent()) }

func extensionSummary(in *oapv1.Intent) string {
	switch c := in.GetChange().(type) {
	case *oapv1.Intent_InstallExtension:
		return "install extension " + c.InstallExtension.GetName() + " (" + strings.Join(c.InstallExtension.GetPoints(), ", ") + ")"
	case *oapv1.Intent_RemoveExtension:
		return "remove extension " + c.RemoveExtension.GetName()
	}
	return ""
}
