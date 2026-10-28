// Package policy is the policy engine (rewrite doc, section 7): it parses the
// owner's autonomy envelope and judges each proposal against it, returning a
// verdict with reasons. Evaluation is deterministic and computes risk itself;
// nothing an agent claims about risk is used.
package policy

import (
	_ "embed"
	"encoding/json"
	"sort"
	"sync"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

//go:embed envelope.schema.json
var EnvelopeSchema []byte

var (
	schemaOnce sync.Once
	schema     *spec.Schema
	schemaErr  error
)

type envelopeDoc struct {
	Agents map[string]struct {
		Scope struct {
			Applications []string `json:"applications"`
			Components   []string `json:"components"`
		} `json:"scope"`
		May []string `json:"may"`
	} `json:"agents"`
	Invariants []struct {
		MinHealthy *struct {
			Application string `json:"application"`
			Component   string `json:"component"`
			Instances   int32  `json:"instances"`
		} `json:"min_healthy"`
		SnapshotBeforeVolumeChange bool `json:"snapshot_before_volume_change"`
		MaxReleases                *struct {
			PerApplicationPerHour int32 `json:"per_application_per_hour"`
		} `json:"max_releases"`
		WithinQuotas bool `json:"within_quotas"`
	} `json:"invariants"`
	Risk struct {
		Routine   []string `json:"routine"`
		Sensitive []string `json:"sensitive"`
	} `json:"risk"`
	ChangeFacts map[string]string `json:"change_facts"`
	Breakers    struct {
		FailedAppliesPerHour int32 `json:"failed_applies_per_hour"`
	} `json:"breakers"`
}

// ParseEnvelope validates an envelope tree (from policy/envelope.yaml) and
// returns its typed form. Its version is the tree's content hash.
func ParseEnvelope(tree any) (*oapv1.Envelope, []spec.Problem) {
	schemaOnce.Do(func() { schema, schemaErr = spec.CompileSchema(EnvelopeSchema) })
	if schemaErr != nil {
		return nil, []spec.Problem{{Why: spec.WhySchema, Message: schemaErr.Error()}}
	}
	if probs := schema.Validate(tree); len(probs) > 0 {
		return nil, probs
	}
	raw, err := spec.Canonical(tree)
	if err != nil {
		return nil, []spec.Problem{{Why: spec.WhySchema, Message: err.Error()}}
	}
	var doc envelopeDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, []spec.Problem{{Why: spec.WhySchema, Message: err.Error()}}
	}
	version, _ := spec.Hash(tree)
	env := &oapv1.Envelope{Version: version}
	for _, name := range sortedKeys(doc.Agents) {
		a := doc.Agents[name]
		env.Agents = append(env.Agents, &oapv1.AgentPolicy{Agent: name, May: a.May,
			Scope: &oapv1.Scope{Applications: a.Scope.Applications, Components: a.Scope.Components}})
	}
	for _, inv := range doc.Invariants {
		switch {
		case inv.MinHealthy != nil:
			m := inv.MinHealthy
			env.Invariants = append(env.Invariants, &oapv1.Invariant{Rule: &oapv1.Invariant_MinHealthy{MinHealthy: &oapv1.MinHealthy{
				Application: m.Application, Component: m.Component, Instances: m.Instances}}})
		case inv.MaxReleases != nil:
			env.Invariants = append(env.Invariants, &oapv1.Invariant{Rule: &oapv1.Invariant_MaxReleases{MaxReleases: &oapv1.MaxReleases{
				PerApplicationPerHour: inv.MaxReleases.PerApplicationPerHour}}})
		case inv.SnapshotBeforeVolumeChange:
			env.Invariants = append(env.Invariants, &oapv1.Invariant{Rule: &oapv1.Invariant_SnapshotBeforeVolumeChange{SnapshotBeforeVolumeChange: true}})
		case inv.WithinQuotas:
			env.Invariants = append(env.Invariants, &oapv1.Invariant{Rule: &oapv1.Invariant_WithinQuotas{WithinQuotas: true}})
		}
	}
	var problems []spec.Problem
	tiers := map[string]oapv1.Risk{}
	for tier, cats := range map[oapv1.Risk][]string{oapv1.Risk_RISK_ROUTINE: doc.Risk.Routine, oapv1.Risk_RISK_SENSITIVE: doc.Risk.Sensitive} {
		for _, c := range cats {
			if prev, dup := tiers[c]; dup && prev != tier {
				problems = append(problems, spec.Problem{Path: "risk", Why: spec.WhyConflict, Message: c + " is listed as both routine and sensitive"})
			}
			tiers[c] = tier
		}
	}
	for _, c := range sortedKeys(tiers) {
		env.Risk = append(env.Risk, &oapv1.RiskRule{Category: c, Risk: tiers[c]})
	}
	actions := map[string]oapv1.FactAction{"routine": oapv1.FactAction_FACT_ACTION_ROUTINE,
		"sensitive": oapv1.FactAction_FACT_ACTION_SENSITIVE, "deny": oapv1.FactAction_FACT_ACTION_DENY}
	for _, f := range sortedKeys(doc.ChangeFacts) {
		env.ChangeFacts = append(env.ChangeFacts, &oapv1.FactRule{Fact: f, Action: actions[doc.ChangeFacts[f]]})
	}
	if doc.Breakers.FailedAppliesPerHour > 0 {
		env.Breakers = &oapv1.Breakers{FailedAppliesPerHour: doc.Breakers.FailedAppliesPerHour}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return env, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
