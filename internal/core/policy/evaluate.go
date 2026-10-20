package policy

import (
	"fmt"
	"sort"
	"strings"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// Stable why codes for policy reasons.
const (
	WhyUnknownAgent      = "policy.unknown_agent"
	WhyAgentPaused       = "policy.agent_paused"
	WhyBreakerOpen       = "policy.breaker_open"
	WhyApplicationScope  = "policy.application_out_of_scope"
	WhyComponentScope    = "policy.component_out_of_scope"
	WhyNotAllowedAction  = "policy.action_not_in_may"
	WhyMinHealthy        = "policy.invariant_min_healthy"
	WhySnapshotFirst     = "policy.invariant_snapshot_before_volume_change"
	WhyMaxReleases       = "policy.invariant_max_releases"
	WhyQuota             = "policy.invariant_within_quotas"
	WhyIrreversible      = "policy.irreversible"
	WhySensitiveCategory = "policy.sensitive_change"
	WhyFact              = "policy.change_fact"
	WhyWithinEnvelope    = "policy.within_envelope"
)

// Counters are what the engine knows about recent activity.
type Counters struct {
	Paused                bool
	FailedAppliesLastHour int
	ReleasesLastHour      int // for the proposal's application
}

// Input is everything a verdict depends on.
type Input struct {
	Agent    string
	Envelope *oapv1.Envelope
	// Proposal is the compiled proposal. Its verdict, risk and reasons are
	// ignored and recomputed.
	Proposal *oapv1.Proposal
	Kind     string // intent kind, for example "scale"
	Changes  []spec.Change
	Old, New *spec.Application // Old is nil for a new application
	// Irreversible lists why the plan can't be undone (from the planner).
	Irreversible []string
	Counters     Counters
	// MemoryBudgetMiB is the memory this application may use, if known.
	MemoryBudgetMiB int
}

// Decision is the policy engine's verdict.
type Decision struct {
	Verdict oapv1.Verdict
	Risk    oapv1.Risk
	Reasons []*oapv1.Reason
	// Next lists what the caller may do next: "apply", "ask", or nothing.
	Next []string
}

type judge struct {
	deny, ask []*oapv1.Reason
	risk      oapv1.Risk
}

func (j *judge) denied(why, format string, args ...any) {
	j.deny = append(j.deny, &oapv1.Reason{Why: why, Message: fmt.Sprintf(format, args...)})
}

func (j *judge) needsOwner(why, format string, args ...any) {
	j.ask = append(j.ask, &oapv1.Reason{Why: why, Message: fmt.Sprintf(format, args...)})
}

func (j *judge) raise(r oapv1.Risk) {
	if r > j.risk {
		j.risk = r
	}
}

// Evaluate judges a proposal. The same input always gives the same decision.
func Evaluate(in Input) Decision {
	j := &judge{risk: oapv1.Risk_RISK_ROUTINE}
	policy := agentPolicy(in.Envelope, in.Agent)
	app := in.Proposal.GetIntent().GetApplication()
	switch {
	case policy == nil:
		j.denied(WhyUnknownAgent, "agent %q is not in the envelope", in.Agent)
	case in.Counters.Paused:
		j.denied(WhyAgentPaused, "agent %q is paused by the owner", in.Agent)
	default:
		if b := in.Envelope.GetBreakers(); b != nil && b.FailedAppliesPerHour > 0 &&
			in.Counters.FailedAppliesLastHour >= int(b.FailedAppliesPerHour) {
			j.denied(WhyBreakerOpen, "%d failed applies in the last hour; the limit is %d",
				in.Counters.FailedAppliesLastHour, b.FailedAppliesPerHour)
		}
		j.scope(policy, app, in.Proposal.GetBlastRadius().GetComponents(), in.Kind)
	}
	if in.New != nil { // server-wide changes (extensions) have no application invariants
		j.invariants(in, app)
	}
	j.categories(in)
	j.facts(in)
	for _, r := range in.Irreversible {
		j.raise(oapv1.Risk_RISK_IRREVERSIBLE)
		j.needsOwner(WhyIrreversible, "%s; irreversible changes always need the owner", r)
	}
	d := Decision{Risk: j.risk}
	switch {
	case len(j.deny) > 0:
		d.Verdict = oapv1.Verdict_VERDICT_DENIED
		d.Reasons = j.deny
	case len(j.ask) > 0:
		d.Verdict = oapv1.Verdict_VERDICT_NEEDS_APPROVAL
		d.Reasons = j.ask
		d.Next = []string{"ask"}
	default:
		d.Verdict = oapv1.Verdict_VERDICT_ALLOWED
		d.Reasons = []*oapv1.Reason{{Why: WhyWithinEnvelope, Message: "within the agent's scope, invariants and risk limits"}}
		d.Next = []string{"apply"}
	}
	sort.SliceStable(d.Reasons, func(a, b int) bool {
		if d.Reasons[a].Why != d.Reasons[b].Why {
			return d.Reasons[a].Why < d.Reasons[b].Why
		}
		return d.Reasons[a].Message < d.Reasons[b].Message
	})
	return d
}

func agentPolicy(env *oapv1.Envelope, agent string) *oapv1.AgentPolicy {
	for _, a := range env.GetAgents() {
		if a.Agent == agent {
			return a
		}
	}
	return nil
}

func listed(list []string, name string) bool {
	for _, n := range list {
		if n == "*" || n == name {
			return true
		}
	}
	return false
}

// scope checks the agent's scope by C4 level, then its allowed actions.
// Omitting components puts every component of an in-scope application in scope.
func (j *judge) scope(p *oapv1.AgentPolicy, app string, components []string, kind string) {
	if !listed(p.Scope.GetApplications(), app) {
		j.denied(WhyApplicationScope, "application %s is outside agent %s's scope", app, p.Agent)
		return
	}
	if allowed := p.Scope.GetComponents(); len(allowed) > 0 {
		for _, c := range components {
			if !listed(allowed, c) {
				j.denied(WhyComponentScope, "component %s is outside agent %s's scope", c, p.Agent)
			}
		}
	}
	if !listed(p.May, kind) {
		j.raise(oapv1.Risk_RISK_SENSITIVE)
		j.needsOwner(WhyNotAllowedAction, "agent %s may not %s without the owner", p.Agent, kind)
	}
}

func (j *judge) invariants(in Input, app string) {
	steps := in.Proposal.GetSteps()
	for _, inv := range in.Envelope.GetInvariants() {
		switch r := inv.Rule.(type) {
		case *oapv1.Invariant_MinHealthy:
			m := r.MinHealthy
			if m.Application != app {
				continue
			}
			want := int(m.Instances)
			if c, ok := in.New.Components[m.Component]; !ok || c.Instances < want {
				j.denied(WhyMinHealthy, "%s.%s must keep at least %d instances", app, m.Component, want)
			} else if low := lowestReady(in.Old, m.Component, steps); low < want {
				j.denied(WhyMinHealthy, "the plan drops %s.%s to %d ready instances; at least %d must stay up", app, m.Component, low, want)
			}
		case *oapv1.Invariant_SnapshotBeforeVolumeChange:
			if s := unsnapshotted(in.Old, steps); s != "" {
				j.denied(WhySnapshotFirst, "%s without a fresh snapshot first", s)
			}
		case *oapv1.Invariant_MaxReleases:
			limit := int(r.MaxReleases.PerApplicationPerHour)
			if releases(steps) && in.Counters.ReleasesLastHour >= limit {
				j.denied(WhyMaxReleases, "%s had %d releases in the last hour; the limit is %d", app, in.Counters.ReleasesLastHour, limit)
			}
		case *oapv1.Invariant_WithinQuotas:
			if in.MemoryBudgetMiB > 0 {
				if need := memory(in.New); need > in.MemoryBudgetMiB {
					j.denied(WhyQuota, "%s would reserve %d MiB; its budget is %d MiB", app, need, in.MemoryBudgetMiB)
				}
			}
		}
	}
}

// lowestReady returns the fewest ready instances of a component at any point
// while the steps run in order, starting from the old desired state.
func lowestReady(old *spec.Application, component string, steps []*oapv1.Step) int {
	ready := 0
	if old != nil {
		ready = old.Components[component].Instances
	}
	low := ready
	for _, s := range steps {
		if s.Component != component {
			continue
		}
		switch s.Kind {
		case oapv1.StepKind_STEP_KIND_AWAIT_READY:
			ready++
		case oapv1.StepKind_STEP_KIND_STOP, oapv1.StepKind_STEP_KIND_DRAIN:
			if s.Kind == oapv1.StepKind_STEP_KIND_DRAIN || !drained(steps, s) {
				ready--
			}
		}
		low = min(low, ready)
	}
	return low
}

// drained reports whether a stop step was preceded by a drain of the same
// instance, which already counted it as no longer ready.
func drained(steps []*oapv1.Step, stop *oapv1.Step) bool {
	for _, s := range steps {
		if s == stop {
			return false
		}
		if s.Kind == oapv1.StepKind_STEP_KIND_DRAIN && s.Component == stop.Component &&
			s.Ordinal == stop.Ordinal && s.Release == stop.Release {
			return true
		}
	}
	return false
}

// unsnapshotted returns a description of the first step that stops an
// instance with volumes, or deletes a volume, before every one of the
// component's volumes was snapshotted in this plan.
func unsnapshotted(old *spec.Application, steps []*oapv1.Step) string {
	snapped := map[string]bool{}
	for _, s := range steps {
		switch s.Kind {
		case oapv1.StepKind_STEP_KIND_SNAPSHOT_VOLUME:
			snapped[s.Component+"/"+s.Volume] = true
		case oapv1.StepKind_STEP_KIND_STOP, oapv1.StepKind_STEP_KIND_DELETE_VOLUME:
			if old == nil {
				continue
			}
			for v := range old.Components[s.Component].Volumes {
				if !snapped[s.Component+"/"+v] {
					return fmt.Sprintf("step %s on %s touches volume %s", strings.ToLower(strings.TrimPrefix(s.Kind.String(), "STEP_KIND_")), s.Component, v)
				}
			}
		}
	}
	return ""
}

func releases(steps []*oapv1.Step) bool {
	for _, s := range steps {
		if s.Kind == oapv1.StepKind_STEP_KIND_START_INSTANCE || s.Kind == oapv1.StepKind_STEP_KIND_RUN_JOB {
			return true
		}
	}
	return false
}

func memory(app *spec.Application) int {
	total := 0
	for _, c := range app.Components {
		if c.Resources != nil && c.Kind != spec.KindJob {
			total += c.Resources.MemoryMiB * c.Instances
		}
	}
	return total
}

// Default tiers for change categories. The envelope can override any of them.
var defaultTiers = map[string]oapv1.Risk{
	"images": oapv1.Risk_RISK_ROUTINE, "instances": oapv1.Risk_RISK_ROUTINE, "env": oapv1.Risk_RISK_ROUTINE,
	"health": oapv1.Risk_RISK_ROUTINE, "ordering": oapv1.Risk_RISK_ROUTINE, "components-added": oapv1.Risk_RISK_ROUTINE,
	"resources": oapv1.Risk_RISK_SENSITIVE, "secret-bindings": oapv1.Risk_RISK_SENSITIVE,
	"connections": oapv1.Risk_RISK_SENSITIVE, "routes": oapv1.Risk_RISK_SENSITIVE, "domains": oapv1.Risk_RISK_SENSITIVE,
	"volumes": oapv1.Risk_RISK_SENSITIVE, "host-ports": oapv1.Risk_RISK_SENSITIVE,
	"components-removed": oapv1.Risk_RISK_SENSITIVE, "job-flags": oapv1.Risk_RISK_SENSITIVE,
	"security": oapv1.Risk_RISK_SENSITIVE,
	// Installing, upgrading or removing an extension (R4.1).
	"extensions": oapv1.Risk_RISK_SENSITIVE,
}

var fieldCategory = map[string]string{
	"image": "images", "instances": "instances", "env": "env", "resources": "resources",
	"secrets": "secret-bindings", "registry_credential": "secret-bindings", "uses": "connections",
	"port": "connections", "after": "ordering", "volumes": "volumes", "host_port": "host-ports",
	"health": "health", "readiness": "health", "destructive": "job-flags", "security": "security",
}

// Category returns the change category of a spec path. Paths that fit no
// category return "other", which is always sensitive.
func Category(c spec.Change) string {
	parts := strings.SplitN(c.Path, ".", 3)
	switch {
	case parts[0] == "components" && len(parts) == 2:
		if c.Op == spec.OpRemove {
			return "components-removed"
		}
		return "components-added"
	case parts[0] == "components" && len(parts) == 3:
		field, _, _ := strings.Cut(parts[2], ".")
		if cat, ok := fieldCategory[field]; ok {
			return cat
		}
	case parts[0] == "ingress" && len(parts) > 1 && parts[1] == "routes":
		return "routes"
	case parts[0] == "ingress":
		return "domains"
	case parts[0] == "extensions":
		return "extensions"
	}
	return "other"
}

func (j *judge) categories(in Input) {
	tiers := map[string]oapv1.Risk{}
	for k, v := range defaultTiers {
		tiers[k] = v
	}
	for _, r := range in.Envelope.GetRisk() {
		tiers[r.Category] = r.Risk
	}
	seen := map[string]bool{}
	for _, c := range in.Changes {
		cat := Category(c)
		if seen[cat] {
			continue
		}
		seen[cat] = true
		tier, ok := tiers[cat]
		if !ok {
			tier = oapv1.Risk_RISK_SENSITIVE
		}
		j.raise(tier)
		if tier >= oapv1.Risk_RISK_SENSITIVE {
			j.needsOwner(WhySensitiveCategory, "changes %s, which the envelope treats as sensitive", cat)
		}
	}
}

// Facts returns the named change facts that apply to a proposal.
func Facts(f *oapv1.ChangeFacts, kind string) []string {
	if f == nil {
		if kind == "deploy" {
			return []string{"no-change-facts"}
		}
		return nil
	}
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(f.Ci != "green", "ci-not-green")
	add(f.DocsOnly, "docs-only")
	add(f.LockfileChanged, "lockfile-changed")
	add(f.MigrationsChanged, "migrations-changed")
	add(!f.Reviewed, "unreviewed")
	add(!f.Signed, "unsigned")
	return out
}

func (j *judge) facts(in Input) {
	rules := map[string]oapv1.FactAction{}
	for _, r := range in.Envelope.GetChangeFacts() {
		rules[r.Fact] = r.Action
	}
	for _, fact := range Facts(in.Proposal.GetChangeFacts(), in.Kind) {
		switch rules[fact] {
		case oapv1.FactAction_FACT_ACTION_DENY:
			j.denied(WhyFact, "%s, which the envelope denies", fact)
		case oapv1.FactAction_FACT_ACTION_SENSITIVE:
			j.raise(oapv1.Risk_RISK_SENSITIVE)
			j.needsOwner(WhyFact, "%s, which the envelope treats as sensitive", fact)
		}
	}
}
