package spec

import (
	"fmt"
	"sort"
)

// Problem is one reason a document was rejected. Why is a stable code agents
// can branch on; Message is for people.
type Problem struct {
	Path    string
	Why     string
	Message string
	// Line and Column locate the problem in the source, when known (1-based).
	Line, Column int
}

func (p Problem) String() string {
	at := p.Path
	if p.Line > 0 {
		at = fmt.Sprintf("%d:%d %s", p.Line, p.Column, p.Path)
	}
	if at == "" {
		return p.Why + ": " + p.Message
	}
	return at + ": " + p.Why + ": " + p.Message
}

// Stable why codes for desired-state documents.
const (
	WhyJSONSyntax        = "spec.json_syntax"
	WhyYAMLSyntax        = "spec.yaml_syntax"
	WhyMultipleDocuments = "spec.yaml_multiple_documents"
	WhyDuplicateKey      = "spec.duplicate_key"
	WhyAnchor            = "spec.yaml_anchor"
	WhyAlias             = "spec.yaml_alias"
	WhyMergeKey          = "spec.yaml_merge_key"
	WhyTag               = "spec.yaml_tag"
	WhyNonStringKey      = "spec.non_string_key"
	WhyAmbiguousBoolean  = "spec.ambiguous_boolean"
	WhyAmbiguousScalar   = "spec.ambiguous_scalar"
	WhyFloat             = "spec.float"
	WhyNull              = "spec.null"
	WhyNonDecimalInteger = "spec.non_decimal_integer"
	WhyIntegerRange      = "spec.integer_out_of_range"
	WhySchema            = "spec.schema"
	WhyUnknownField      = "spec.unknown_field"
	WhyRequired          = "spec.required"
	WhyType              = "spec.type"
	WhyEnum              = "spec.enum"
	WhyPattern           = "spec.pattern"
	WhyRange             = "spec.range"
	WhyLength            = "spec.length"
	WhyUnknownComponent  = "spec.unknown_component"
	WhyKindRule          = "spec.kind_rule"
	WhyConflict          = "spec.conflict"
	WhyCycle             = "spec.dependency_cycle"
	WhyTooLarge          = "spec.too_large"
	WhyLegacyUnsupported = "convert.unsupported"
	WhyLegacyFormat      = "convert.format"
)

// Sort orders problems by position, then path, then code, so output is stable.
func Sort(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Why < b.Why
	})
}
