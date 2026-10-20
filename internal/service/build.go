package service

import (
	"context"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/intent"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/proposal"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
)

// Builds (R4.2). A build is a proposal within the application's scope,
// routine by default, whose operation runs one build step through the
// server's build queue. It changes no desired state: the outcome names the
// image by component, and a deploy rolls it out.

// proposeBuild is Propose for build intents. The caller holds s.mu and
// has checked the application's scope.
func (s *Service) proposeBuild(ctx context.Context, c Caller, in *oapv1.Intent, head uint64) (*oapv1.ProposeResponse, error) {
	tree, err := s.current(ctx, in.GetApplication(), false)
	if err != nil {
		return nil, err
	}
	res, ierr := intent.CompileBuild(tree, in)
	if ierr != nil {
		return nil, &Error{Code: InvalidArgument, Detail: ierr.Detail()}
	}
	env, err := s.envelope(ctx)
	if err != nil {
		return nil, err
	}
	hash, _ := spec.Hash(tree)
	out := &oapv1.Proposal{BaseRevision: head, BaseSpecHash: hash, Agent: c.Agent, Intent: in, EnvelopeVersion: env.GetVersion(),
		Steps:   []*oapv1.Step{{Kind: oapv1.StepKind_STEP_KIND_BUILD, Component: res.Component, Image: res.Image}},
		Context: &oapv1.PolicyContext{}}
	d := policy.Evaluate(policy.Input{Agent: c.Agent, Envelope: env, Proposal: out, Kind: string(res.Kind()), Changes: res.Changes})
	out.Verdict, out.Risk, out.Reasons, out.Next = d.Verdict, d.Risk, d.Reasons, d.Next
	if out.Id, err = proposal.ID(out); err != nil {
		return nil, err
	}
	if _, err := s.append(ctx, KindProposal, "", &oapv1.JournalEntry{Id: out.Id, Kind: oapv1.EntryKind_ENTRY_KIND_PROPOSAL,
		Actor: actor(c), BaseRevision: head, Summary: summary(in), Detail: &oapv1.JournalEntry_Proposal{Proposal: out}}); err != nil {
		return nil, err
	}
	return &oapv1.ProposeResponse{Proposal: out}, nil
}

func isBuildOp(p *oapv1.Proposal) bool { return p.GetIntent().GetBuild() != nil }

// runBuild runs a build operation in the background.
func (s *Service) runBuild(id string, p *oapv1.Proposal) {
	ctx := context.WithoutCancel(s.runCtx)
	go func() {
		images, err := s.build(ctx, id, p)
		s.outcomeWith(id, err, images)
	}()
}

func (s *Service) build(ctx context.Context, id string, p *oapv1.Proposal) (map[string]string, error) {
	step := p.GetSteps()[0]
	fail := func(reason string) error {
		s.recordStep(ctx, id, 0, engine.StateFailed, reason)
		return &engine.StepError{Step: 0, Kind: step.GetKind(), Reason: reason}
	}
	if s.Builder == nil {
		return nil, fail("this server has no build queue")
	}
	tree, err := s.current(ctx, p.GetIntent().GetApplication(), false)
	if err != nil {
		return nil, fail(err.Error())
	}
	res, ierr := intent.CompileBuild(tree, p.GetIntent())
	if ierr != nil {
		return nil, fail(ierr.Message)
	}
	out, err := s.Builder.Build(ctx, ops.BuildRequest{Application: p.GetIntent().GetApplication(), Component: res.Component,
		Repository: res.Repository, Commit: res.Commit, Context: res.Context, Dockerfile: res.Dockerfile,
		Image: res.Image, MemoryMiB: res.MemoryMiB, Builder: res.Builder,
		// The step starts when the queue lets the build run.
		Started: func() { s.recordStep(ctx, id, 0, engine.StateStarted, "") }})
	if err != nil {
		return nil, fail(err.Error())
	}
	s.recordStep(ctx, id, 0, engine.StateSucceeded, "built "+out.Image+" from "+out.Commit)
	return map[string]string{res.Component: out.Image}, nil
}
