package policy

import (
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
)

// Kinds lists every intent kind, in the order affordances are reported.
var Kinds = []string{"scale", "deploy", "rollback", "configure", "add-component", "remove-component", "spec-patch"}

// componentKinds are the intent kinds that act on one component.
var componentKinds = map[string]bool{"scale": true, "configure": true, "remove-component": true}

// Affordances fills the world model's affordance slots for one agent: what it
// may propose on each application and component, judged by scope and allowed
// actions only. A proposal's own verdict still decides; an affordance of
// "allowed" means the policy engine won't refuse it for scope or action.
func Affordances(env *oapv1.Envelope, agent string, w *oapv1.World) {
	p := agentPolicy(env, agent)
	for _, app := range w.GetApplications() {
		app.Affordances = nil
		for _, kind := range Kinds {
			if !componentKinds[kind] {
				app.Affordances = append(app.Affordances, &oapv1.Affordance{Action: kind, Verdict: affordance(p, app.Name, "", kind)})
			}
		}
		for _, c := range app.Components {
			c.Affordances = nil
			for _, kind := range Kinds {
				if componentKinds[kind] {
					c.Affordances = append(c.Affordances, &oapv1.Affordance{Action: kind, Verdict: affordance(p, app.Name, c.Name, kind)})
				}
			}
		}
	}
}

func affordance(p *oapv1.AgentPolicy, app, component, kind string) oapv1.Verdict {
	j := &judge{}
	if p == nil {
		return oapv1.Verdict_VERDICT_DENIED
	}
	var components []string
	if component != "" {
		components = []string{component}
	}
	j.scope(p, app, components, kind)
	switch {
	case len(j.deny) > 0:
		return oapv1.Verdict_VERDICT_DENIED
	case len(j.ask) > 0:
		return oapv1.Verdict_VERDICT_NEEDS_APPROVAL
	}
	return oapv1.Verdict_VERDICT_ALLOWED
}
