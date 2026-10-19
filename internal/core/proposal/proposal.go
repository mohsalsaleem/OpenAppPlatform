// Package proposal assembles a proposal from an intent: compile, plan, judge,
// then name it by a content hash over everything the result depends on.
// Replaying a stored proposal runs the same pipeline from its recorded inputs
// and must give back the identical proposal.
package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/plan"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Inputs is everything a proposal depends on.
type Inputs struct {
	Agent        string
	BaseRevision uint64
	// Tree is the application's current desired state.
	Tree any
	// History looks up earlier desired states by hash, for rollback.
	History  func(hash string) (any, bool)
	Intent   *oapv1.Intent
	Facts    *oapv1.ChangeFacts
	Envelope *oapv1.Envelope
	Context  *oapv1.PolicyContext
}

// Propose compiles, plans and judges an intent.
func Propose(in Inputs) (*oapv1.Proposal, *intent.Error) {
	res, err := intent.Compile(intent.Current{Tree: in.Tree, History: in.History}, in.Intent)
	if err != nil {
		return nil, err
	}
	old, _ := spec.ParseApplication(in.Tree)
	baseHash, herr := spec.Hash(in.Tree)
	if herr != nil {
		return nil, &intent.Error{Why: intent.WhyBadValue, Message: herr.Error()}
	}
	p := plan.Build(plan.Input{Application: res.App.Name, Old: old, OldTree: in.Tree, OldHash: baseHash,
		New: res.App, NewTree: res.Tree, Rollback: res.Rollback, DeleteVolumes: res.DeleteVolumes, Intent: in.Intent})
	ctx := in.Context
	if ctx == nil {
		ctx = &oapv1.PolicyContext{}
	}
	out := &oapv1.Proposal{
		BaseRevision:    in.BaseRevision,
		BaseSpecHash:    baseHash,
		Agent:           in.Agent,
		Intent:          proto.Clone(in.Intent).(*oapv1.Intent),
		Steps:           p.Steps,
		BlastRadius:     p.BlastRadius,
		Undo:            p.Undo,
		Irreversible:    p.Irreversible,
		EnvelopeVersion: in.Envelope.GetVersion(),
		ChangeFacts:     cloneFacts(in.Facts),
		Context:         proto.Clone(ctx).(*oapv1.PolicyContext),
	}
	d := policy.Evaluate(policy.Input{
		Agent: in.Agent, Envelope: in.Envelope, Proposal: out, Kind: string(res.Kind), Changes: res.Changes,
		Old: old, New: res.App, Irreversible: p.Irreversible,
		Counters: policy.Counters{Paused: ctx.Paused, FailedAppliesLastHour: int(ctx.FailedAppliesLastHour),
			ReleasesLastHour: int(ctx.ReleasesLastHour)},
		MemoryBudgetMiB: int(ctx.MemoryBudgetMib),
	})
	out.Verdict, out.Risk, out.Reasons, out.Next = d.Verdict, d.Risk, d.Reasons, d.Next
	id, idErr := ID(out)
	if idErr != nil {
		return nil, &intent.Error{Why: intent.WhyBadValue, Message: idErr.Error()}
	}
	out.Id = id
	return out, nil
}

func cloneFacts(f *oapv1.ChangeFacts) *oapv1.ChangeFacts {
	if f == nil {
		return nil
	}
	return proto.Clone(f).(*oapv1.ChangeFacts)
}

// ID returns a proposal's content hash over its identity fields: every input
// the plan and verdict depend on. Outputs (verdict, risk, reasons, undo, blast
// radius) are derived from these and left out. The hash uses canonical JSON,
// so it is stable across protobuf library versions.
func ID(p *oapv1.Proposal) (string, error) {
	identity := &oapv1.Proposal{
		BaseRevision:    p.BaseRevision,
		BaseSpecHash:    p.BaseSpecHash,
		Agent:           p.Agent,
		Intent:          p.Intent,
		Steps:           p.Steps,
		ChangeFacts:     p.ChangeFacts,
		EnvelopeVersion: p.EnvelopeVersion,
		Context:         p.Context,
	}
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(identity)
	if err != nil {
		return "", err
	}
	tree, probs := spec.FromJSON(raw)
	if len(probs) > 0 {
		return "", fmt.Errorf("proposal: %v", probs[0])
	}
	canonical, err := spec.Canonical(tree)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "prop_" + hex.EncodeToString(sum[:16]), nil
}

// Stores look up what a stored proposal refers to.
type Stores struct {
	// Desired returns the desired state with this spec hash.
	Desired func(hash string) (any, bool)
	// Envelope returns the envelope with this version.
	Envelope func(version string) (*oapv1.Envelope, bool)
}

// Why codes for replay failures.
const (
	WhyTampered        = "proposal.tampered"
	WhyUnknownBase     = "proposal.unknown_base"
	WhyUnknownEnvelope = "proposal.unknown_envelope"
	WhyNotReproduced   = "proposal.not_reproduced"
)

// Replay re-runs a stored proposal from its recorded inputs and checks that
// the result is identical: same id, steps, verdict, risk and reasons.
func Replay(stored *oapv1.Proposal, s Stores) error {
	id, err := ID(stored)
	if err != nil {
		return err
	}
	if id != stored.Id {
		return &intent.Error{Why: WhyTampered, Message: "the proposal's inputs don't match its id"}
	}
	tree, ok := s.Desired(stored.BaseSpecHash)
	if !ok {
		return &intent.Error{Why: WhyUnknownBase, Message: "no desired state with hash " + stored.BaseSpecHash}
	}
	env, ok := s.Envelope(stored.EnvelopeVersion)
	if !ok {
		return &intent.Error{Why: WhyUnknownEnvelope, Message: "no envelope with version " + stored.EnvelopeVersion}
	}
	again, perr := Propose(Inputs{Agent: stored.Agent, BaseRevision: stored.BaseRevision, Tree: tree,
		History: s.Desired, Intent: stored.Intent, Facts: stored.ChangeFacts, Envelope: env, Context: stored.Context})
	if perr != nil {
		return perr
	}
	if !proto.Equal(again, stored) {
		return &intent.Error{Why: WhyNotReproduced, Message: "replaying the proposal gave a different result"}
	}
	return nil
}
