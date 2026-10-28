// Package desiredyaml reads and writes OAP's desired-state YAML: a strict
// subset of YAML that means the same thing to every YAML parser. Documents
// decode into spec trees (see internal/core/spec).
//
// Rejected: multiple documents, anchors, aliases, merge keys, explicit tags,
// non-string keys, duplicate keys, nulls, floats, non-decimal integers, and
// plain scalars that YAML 1.1 parsers read differently (yes/no/on/off,
// sexagesimal numbers, dates). Quote such values to use them as strings.
package desiredyaml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"gopkg.in/yaml.v3"
)

var (
	decimal     = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	sexagesimal = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(:[0-5]?[0-9])+(\.[0-9_]*)?$`)
	yaml11Bool  = map[string]bool{"y": true, "n": true, "yes": true, "no": true, "on": true, "off": true}
)

// Parse decodes a desired-state YAML document into a tree.
func Parse(b []byte) (any, []spec.Problem) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []spec.Problem{{Why: spec.WhyYAMLSyntax, Message: "empty document"}}
		}
		return nil, []spec.Problem{{Why: spec.WhyYAMLSyntax, Message: err.Error()}}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, []spec.Problem{{Why: spec.WhyMultipleDocuments, Message: "one document per file", Line: extra.Line}}
	}
	if len(doc.Content) != 1 {
		return nil, []spec.Problem{{Why: spec.WhyYAMLSyntax, Message: "empty document"}}
	}
	p := &parser{}
	tree := p.value(doc.Content[0], "")
	if len(p.problems) > 0 {
		spec.Sort(p.problems)
		return nil, p.problems
	}
	return tree, nil
}

type parser struct{ problems []spec.Problem }

func (p *parser) fail(n *yaml.Node, path, why, format string, args ...any) {
	p.problems = append(p.problems, spec.Problem{Path: path, Why: why, Message: fmt.Sprintf(format, args...),
		Line: n.Line, Column: n.Column})
}

func (p *parser) value(n *yaml.Node, path string) any {
	if n.Anchor != "" {
		p.fail(n, path, spec.WhyAnchor, "anchors are not allowed")
	}
	if n.Style&yaml.TaggedStyle != 0 {
		p.fail(n, path, spec.WhyTag, "explicit tags are not allowed")
		return nil
	}
	switch n.Kind {
	case yaml.AliasNode:
		p.fail(n, path, spec.WhyAlias, "aliases are not allowed")
		return nil
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			key, ok := p.key(k, path)
			if !ok {
				continue
			}
			child := spec.Join(path, key)
			if _, dup := m[key]; dup {
				p.fail(k, child, spec.WhyDuplicateKey, "key appears more than once")
				continue
			}
			m[key] = p.value(v, child)
		}
		return m
	case yaml.SequenceNode:
		l := make([]any, 0, len(n.Content))
		for i, e := range n.Content {
			l = append(l, p.value(e, spec.Index(path, i)))
		}
		return l
	case yaml.ScalarNode:
		return p.scalar(n, path)
	}
	p.fail(n, path, spec.WhyYAMLSyntax, "unexpected node")
	return nil
}

func (p *parser) key(k *yaml.Node, path string) (string, bool) {
	if k.Kind != yaml.ScalarNode || k.Style&yaml.TaggedStyle != 0 || k.Anchor != "" {
		p.fail(k, path, spec.WhyNonStringKey, "keys must be plain or quoted strings")
		return "", false
	}
	if k.Tag == "!!merge" {
		p.fail(k, path, spec.WhyMergeKey, "merge keys are not allowed")
		return "", false
	}
	v := p.scalar(k, spec.Join(path, k.Value))
	s, ok := v.(string)
	if !ok {
		if v != nil {
			p.fail(k, spec.Join(path, k.Value), spec.WhyNonStringKey, "keys must be strings; quote %q", k.Value)
		}
		return "", false
	}
	return s, true
}

func (p *parser) scalar(n *yaml.Node, path string) any {
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return n.Value
	}
	switch n.Tag {
	case "!!str":
		lower := strings.ToLower(n.Value)
		if yaml11Bool[lower] {
			p.fail(n, path, spec.WhyAmbiguousBoolean, "%q is a boolean to YAML 1.1 parsers; quote it, or use true or false", n.Value)
			return nil
		}
		if sexagesimal.MatchString(n.Value) {
			p.fail(n, path, spec.WhyAmbiguousScalar, "%q is a number to YAML 1.1 parsers; quote it", n.Value)
			return nil
		}
		return n.Value
	case "!!bool":
		if n.Value != "true" && n.Value != "false" {
			p.fail(n, path, spec.WhyAmbiguousBoolean, "write booleans as true or false, not %q", n.Value)
			return nil
		}
		return n.Value == "true"
	case "!!int":
		if !decimal.MatchString(n.Value) {
			p.fail(n, path, spec.WhyNonDecimalInteger, "write integers in decimal without leading zeros, not %q", n.Value)
			return nil
		}
		i, err := strconv.ParseInt(n.Value, 10, 64)
		if err != nil {
			p.fail(n, path, spec.WhyIntegerRange, "%s does not fit in 64 bits", n.Value)
			return nil
		}
		return i
	case "!!float":
		p.fail(n, path, spec.WhyFloat, "numbers must be integers; quote %q to use it as a string", n.Value)
	case "!!null":
		p.fail(n, path, spec.WhyNull, "null is not allowed; omit the field instead")
	case "!!timestamp":
		p.fail(n, path, spec.WhyAmbiguousScalar, "%q is a date to YAML parsers; quote it", n.Value)
	default:
		p.fail(n, path, spec.WhyTag, "unsupported scalar type %s", n.Tag)
	}
	return nil
}

// Emit writes a tree as canonical desired-state YAML: sorted keys, two-space
// indentation, and plain scalars only where every YAML parser reads them back
// as the same string. Parse(Emit(t)) equals t.
func Emit(tree any) ([]byte, error) {
	m, ok := tree.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("desiredyaml: the top level must be a map, not %T", tree)
	}
	var buf bytes.Buffer
	if len(m) == 0 {
		buf.WriteString("{}\n")
		return buf.Bytes(), nil
	}
	if err := emitMap(&buf, m, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func emitMap(buf *bytes.Buffer, m map[string]any, indent int) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		buf.WriteString(strings.Repeat(" ", indent))
		buf.WriteString(scalarString(k))
		buf.WriteByte(':')
		if err := emitValue(buf, m[k], indent); err != nil {
			return err
		}
	}
	return nil
}

// emitValue writes v after a "key:" or "-" marker at the given indent.
func emitValue(buf *bytes.Buffer, v any, indent int) error {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			buf.WriteString(" {}\n")
			return nil
		}
		buf.WriteByte('\n')
		return emitMap(buf, t, indent+2)
	case []any:
		if len(t) == 0 {
			buf.WriteString(" []\n")
			return nil
		}
		buf.WriteByte('\n')
		for _, e := range t {
			buf.WriteString(strings.Repeat(" ", indent+2))
			buf.WriteByte('-')
			if err := emitItem(buf, e, indent+2); err != nil {
				return err
			}
		}
		return nil
	}
	s, err := scalar(v)
	if err != nil {
		return err
	}
	buf.WriteString(" " + s + "\n")
	return nil
}

// emitItem writes a list item after its "-" marker at the given indent.
func emitItem(buf *bytes.Buffer, v any, indent int) error {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return emitValue(buf, v, indent)
	}
	// A non-empty map starts on the marker's line: "- key: value".
	var inner bytes.Buffer
	if err := emitMap(&inner, m, indent+2); err != nil {
		return err
	}
	buf.WriteByte(' ')
	buf.Write(inner.Bytes()[indent+2:])
	return nil
}

func scalar(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return scalarString(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case bool:
		return strconv.FormatBool(t), nil
	}
	return "", fmt.Errorf("desiredyaml: %T is not a tree value", v)
}

var plainCandidate = regexp.MustCompile(`^[A-Za-z0-9_/][A-Za-z0-9_./:@+=,() -]*$`)

// scalarString returns s plain when it reads back unchanged and unambiguous,
// and double-quoted otherwise.
func scalarString(s string) string {
	if plainCandidate.MatchString(s) && !strings.HasSuffix(s, " ") && !strings.HasSuffix(s, ":") &&
		!strings.Contains(s, ": ") && readsBackAsString(s) {
		return s
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

func readsBackAsString(s string) bool {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("k: "+s), &n); err != nil || len(n.Content) != 1 {
		return false
	}
	m := n.Content[0]
	if len(m.Content) != 2 {
		return false
	}
	p := &parser{}
	v := p.scalar(m.Content[1], "")
	got, ok := v.(string)
	return ok && len(p.problems) == 0 && got == s
}
