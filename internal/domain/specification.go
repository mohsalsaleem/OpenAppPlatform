package domain

import (
	"encoding/json"
	"errors"
	"sort"
)

// ApplicationSpecification wraps the existing manifest without rewriting stored
// definitions or changing native resource ownership. JSON is the initial format.
type ApplicationSpecification struct {
	APIVersion     string              `json:"apiVersion"`
	Kind           string              `json:"kind"`
	Spec           Manifest            `json:"spec"`
	RedactedFields map[string][]string `json:"redactedFields,omitempty"`
}

func (s *ApplicationSpecification) Validate() error {
	if s.APIVersion != "oap.dev/v1alpha1" || s.Kind != "Application" {
		return errors.New("supported specification is oap.dev/v1alpha1 Application")
	}
	if len(s.RedactedFields) > 0 {
		return errors.New("refill omitted literal values before validating or saving an exported specification")
	}
	return s.Spec.Validate()
}
func ExportSpecification(m Manifest) ApplicationSpecification {
	raw, _ := json.Marshal(m)
	var cloned Manifest
	_ = json.Unmarshal(raw, &cloned)
	out := ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: cloned, RedactedFields: map[string][]string{}}
	for i := range out.Spec.Components {
		c := &out.Spec.Components[i]
		for key := range c.Env {
			out.RedactedFields[c.Name] = append(out.RedactedFields[c.Name], "env."+key)
		}
		sort.Strings(out.RedactedFields[c.Name])
		c.Env = nil
	}
	return out
}
func SpecificationDiff(current Application, proposed Manifest) ReleaseComparison {
	return CompareReleases(Deployment{ID: "current", DefinitionVersion: current.Version, Manifest: current.Manifest, Steps: InitialSteps(current.Manifest)}, Deployment{ID: "proposed", DefinitionVersion: current.Version + 1, Manifest: proposed, Steps: InitialSteps(proposed)})
}
