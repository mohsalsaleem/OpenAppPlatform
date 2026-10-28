package spec

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ApplicationSchema is the JSON Schema for application specs. It is published
// to agents and editors; Validate checks documents against it.
//
//go:embed application.schema.json
var ApplicationSchema []byte

// Schema is a compiled JSON Schema. Only the keywords OAP uses are supported,
// and compiling a schema with any other keyword fails, so the schema can't
// silently rely on a rule that is not enforced.
type Schema struct {
	root *node
	defs map[string]*node
}

type node struct {
	ref                  string
	typ                  string
	properties           map[string]*node
	required             []string
	additionalProperties *node // schema for keys not in properties
	denyAdditional       bool  // additionalProperties: false
	propertyNames        *node
	minProperties        *int
	maxProperties        *int
	items                *node
	minItems, maxItems   *int
	enum                 []string
	pattern              *regexp.Regexp
	minLength, maxLength *int
	minimum, maximum     *int64
}

var known = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true, "$defs": true, "$ref": true,
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"propertyNames": true, "minProperties": true, "maxProperties": true, "items": true,
	"minItems": true, "maxItems": true, "enum": true, "pattern": true, "minLength": true,
	"maxLength": true, "minimum": true, "maximum": true,
}

// CompileSchema compiles a JSON Schema document.
func CompileSchema(doc []byte) (*Schema, error) {
	var raw map[string]any
	if err := json.Unmarshal(doc, &raw); err != nil {
		return nil, err
	}
	s := &Schema{defs: map[string]*node{}}
	if defs, ok := raw["$defs"].(map[string]any); ok {
		for name, d := range defs {
			n, err := compile(d, "$defs."+name)
			if err != nil {
				return nil, err
			}
			s.defs[name] = n
		}
	}
	root, err := compile(raw, "")
	if err != nil {
		return nil, err
	}
	s.root = root
	if err := s.checkRefs(root); err != nil {
		return nil, err
	}
	for _, d := range s.defs {
		if err := s.checkRefs(d); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Schema) checkRefs(n *node) error {
	if n == nil {
		return nil
	}
	if _, ok := s.defs[n.ref]; n.ref != "" && !ok {
		return fmt.Errorf("schema: unresolved $ref %q", n.ref)
	}
	for _, c := range n.properties {
		if err := s.checkRefs(c); err != nil {
			return err
		}
	}
	for _, c := range []*node{n.additionalProperties, n.propertyNames, n.items} {
		if err := s.checkRefs(c); err != nil {
			return err
		}
	}
	return nil
}

func compile(v any, at string) (*node, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema %s: not an object", at)
	}
	n := &node{}
	for k, val := range m {
		if !known[k] {
			return nil, fmt.Errorf("schema %s: unsupported keyword %q", at, k)
		}
		var err error
		switch k {
		case "$ref":
			ref, _ := val.(string)
			if !strings.HasPrefix(ref, "#/$defs/") {
				return nil, fmt.Errorf("schema %s: only #/$defs/ references are supported", at)
			}
			n.ref = strings.TrimPrefix(ref, "#/$defs/")
		case "type":
			n.typ, _ = val.(string)
			switch n.typ {
			case "object", "array", "string", "integer", "boolean":
			default:
				return nil, fmt.Errorf("schema %s: unsupported type %v", at, val)
			}
		case "properties":
			props, _ := val.(map[string]any)
			n.properties = map[string]*node{}
			for name, p := range props {
				if n.properties[name], err = compile(p, at+".properties."+name); err != nil {
					return nil, err
				}
			}
		case "required":
			for _, r := range val.([]any) {
				n.required = append(n.required, r.(string))
			}
		case "additionalProperties":
			if b, isBool := val.(bool); isBool {
				n.denyAdditional = !b
			} else if n.additionalProperties, err = compile(val, at+".additionalProperties"); err != nil {
				return nil, err
			}
		case "propertyNames":
			if n.propertyNames, err = compile(val, at+".propertyNames"); err != nil {
				return nil, err
			}
		case "items":
			if n.items, err = compile(val, at+".items"); err != nil {
				return nil, err
			}
		case "enum":
			for _, e := range val.([]any) {
				n.enum = append(n.enum, e.(string))
			}
		case "pattern":
			if n.pattern, err = regexp.Compile(val.(string)); err != nil {
				return nil, fmt.Errorf("schema %s: %w", at, err)
			}
		case "minProperties", "maxProperties", "minItems", "maxItems", "minLength", "maxLength":
			i := int(val.(float64))
			switch k {
			case "minProperties":
				n.minProperties = &i
			case "maxProperties":
				n.maxProperties = &i
			case "minItems":
				n.minItems = &i
			case "maxItems":
				n.maxItems = &i
			case "minLength":
				n.minLength = &i
			case "maxLength":
				n.maxLength = &i
			}
		case "minimum":
			i := int64(val.(float64))
			n.minimum = &i
		case "maximum":
			i := int64(val.(float64))
			n.maximum = &i
		}
	}
	return n, nil
}

// Validate checks a tree against the schema and returns every problem found.
func (s *Schema) Validate(tree any) []Problem {
	var out []Problem
	s.validate(s.root, tree, "", &out)
	Sort(out)
	return out
}

func (s *Schema) validate(n *node, v any, path string, out *[]Problem) {
	if n.ref != "" {
		s.validate(s.defs[n.ref], v, path, out)
		return
	}
	add := func(why, format string, args ...any) {
		*out = append(*out, Problem{Path: path, Why: why, Message: fmt.Sprintf(format, args...)})
	}
	if n.typ != "" && typeOf(v) != n.typ {
		add(WhyType, "want %s, got %s", n.typ, typeOf(v))
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for _, r := range n.required {
			if _, ok := t[r]; !ok {
				*out = append(*out, Problem{Path: Join(path, r), Why: WhyRequired, Message: "required"})
			}
		}
		if n.minProperties != nil && len(t) < *n.minProperties {
			add(WhyLength, "at least %d entries", *n.minProperties)
		}
		if n.maxProperties != nil && len(t) > *n.maxProperties {
			add(WhyLength, "at most %d entries", *n.maxProperties)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := Join(path, k)
			if n.propertyNames != nil {
				var keyProbs []Problem
				s.validate(n.propertyNames, k, child, &keyProbs)
				for _, p := range keyProbs {
					p.Message = "key: " + p.Message
					*out = append(*out, p)
				}
			}
			if p, ok := n.properties[k]; ok {
				s.validate(p, t[k], child, out)
			} else if n.additionalProperties != nil {
				s.validate(n.additionalProperties, t[k], child, out)
			} else if n.denyAdditional {
				*out = append(*out, Problem{Path: child, Why: WhyUnknownField, Message: "unknown field"})
			}
		}
	case []any:
		if n.minItems != nil && len(t) < *n.minItems {
			add(WhyLength, "at least %d items", *n.minItems)
		}
		if n.maxItems != nil && len(t) > *n.maxItems {
			add(WhyLength, "at most %d items", *n.maxItems)
		}
		if n.items != nil {
			for i, e := range t {
				s.validate(n.items, e, Index(path, i), out)
			}
		}
	case string:
		if len(n.enum) > 0 && !contains(n.enum, t) {
			add(WhyEnum, "want one of %s", strings.Join(n.enum, ", "))
		}
		length := utf8.RuneCountInString(t)
		if n.minLength != nil && length < *n.minLength {
			add(WhyLength, "at least %d characters", *n.minLength)
		}
		if n.maxLength != nil && length > *n.maxLength {
			add(WhyLength, "at most %d characters", *n.maxLength)
		}
		if n.pattern != nil && !n.pattern.MatchString(t) {
			add(WhyPattern, "must match %s", n.pattern)
		}
	case int64:
		if n.minimum != nil && t < *n.minimum {
			add(WhyRange, "at least %d", *n.minimum)
		}
		if n.maximum != nil && t > *n.maximum {
			add(WhyRange, "at most %d", *n.maximum)
		}
	}
}

func typeOf(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case int64:
		return "integer"
	case bool:
		return "boolean"
	}
	return fmt.Sprintf("%T", v)
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
