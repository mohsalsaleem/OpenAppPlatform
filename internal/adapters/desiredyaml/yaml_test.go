package desiredyaml

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

func TestParseRejectsOutsideTheSubset(t *testing.T) {
	cases := []struct{ name, doc, why string }{
		{"two documents", "a: 1\n---\nb: 2\n", spec.WhyMultipleDocuments},
		{"anchor", "a: &x 1\nb: 2\n", spec.WhyAnchor},
		{"alias", "a: &x {c: 1}\nb: *x\n", spec.WhyAnchor},
		{"merge key", "base: {c: 1}\nb:\n  <<: {c: 1}\n", spec.WhyMergeKey},
		{"explicit tag", "a: !!str 1\n", spec.WhyTag},
		{"custom tag", "a: !secret x\n", spec.WhyTag},
		{"integer key", "8080: web\n", spec.WhyNonStringKey},
		{"boolean key", "true: x\n", spec.WhyNonStringKey},
		{"yaml 1.1 key", "on: push\n", spec.WhyAmbiguousBoolean},
		{"duplicate key", "a: 1\na: 2\n", spec.WhyDuplicateKey},
		{"yes", "https: yes\n", spec.WhyAmbiguousBoolean},
		{"Off", "https: Off\n", spec.WhyAmbiguousBoolean},
		{"True", "https: True\n", spec.WhyAmbiguousBoolean},
		{"float", "cpu: 0.5\n", spec.WhyFloat},
		{"exponent", "cpu: 1e3\n", spec.WhyFloat},
		{"infinity", "cpu: .inf\n", spec.WhyFloat},
		{"null", "image: null\n", spec.WhyNull},
		{"tilde", "image: ~\n", spec.WhyNull},
		{"empty value", "image:\n", spec.WhyNull},
		{"octal", "mode: 0o755\n", spec.WhyNonDecimalInteger},
		{"leading zero", "mode: 0755\n", spec.WhyNonDecimalInteger},
		{"hex", "port: 0x1F90\n", spec.WhyNonDecimalInteger},
		{"too big", "count: 99999999999999999999\n", spec.WhyFloat},
		{"sexagesimal", "t: 1:30\n", spec.WhyAmbiguousScalar},
		{"date", "d: 2026-10-19\n", spec.WhyAmbiguousScalar},
		{"syntax", "a: [1\n", spec.WhyYAMLSyntax},
		{"empty", "", spec.WhyYAMLSyntax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs := Parse([]byte(c.doc))
			for _, p := range probs {
				if p.Why == c.why {
					if c.why != spec.WhyYAMLSyntax && p.Line == 0 && c.why != spec.WhyMultipleDocuments {
						t.Errorf("problem has no line: %v", p)
					}
					return
				}
			}
			t.Fatalf("want %s, got %v", c.why, probs)
		})
	}
}

func TestParseAcceptsQuotedLookalikes(t *testing.T) {
	doc := `a: "yes"
b: 'on'
c: "0.5"
d: "2026-10-19"
e: "1:30"
f: "~"
g: |
  multi
  line
h: nginx:1.27-alpine
i: -42
j: false
"8080": web
`
	tree, probs := Parse([]byte(doc))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	m := tree.(map[string]any)
	want := map[string]any{"a": "yes", "b": "on", "c": "0.5", "d": "2026-10-19", "e": "1:30", "f": "~",
		"g": "multi\nline\n", "h": "nginx:1.27-alpine", "i": int64(-42), "j": false, "8080": "web"}
	if !spec.Equal(m, want) {
		t.Fatalf("got %#v", m)
	}
}

var tricky = []string{
	"", " ", "x", "yes", "No", "ON", "true", "False", "null", "~", "1", "-1", "0755", "0x10", "1.5", "1e3",
	".inf", "1:30", "2026-10-19", "nginx:1.27-alpine", "a: b", "a:b", "# hash", "a #b", "- dash", "-x",
	"/", "/api", "/a/b", "trailing ", " leading", "tab\there", "line\nbreak", `quote"`, "back\\slash",
	"uni ✓", "{x}", "[x]", "&a", "*a", "!t", "%p", "@at", "`tick`", "a, b", "key=value", "UPPER_CASE",
	"ghcr.io/acme/api@sha256:0123456789abcdef", "postgres://db:5432/app", "\x00nul", "?", ":", "a:",
}

func randomTree(r *rand.Rand, depth int) any {
	switch k := r.IntN(10); {
	case depth > 3 || k < 4:
		switch r.IntN(4) {
		case 0:
			return tricky[r.IntN(len(tricky))]
		case 1:
			return int64(r.IntN(200000) - 100000)
		case 2:
			return r.IntN(2) == 0
		default:
			return fmt.Sprintf("v%d", r.IntN(100))
		}
	case k < 7:
		m := map[string]any{}
		for i := r.IntN(4); i > 0; i-- {
			m[tricky[r.IntN(len(tricky))]] = randomTree(r, depth+1)
		}
		return m
	default:
		l := []any{}
		for i := r.IntN(4); i > 0; i-- {
			l = append(l, randomTree(r, depth+1))
		}
		return l
	}
}

func TestEmitParseRoundTrip(t *testing.T) {
	const runs = 3000
	for seed := uint64(1); seed <= runs; seed++ {
		r := rand.New(rand.NewPCG(seed, 0))
		tree := map[string]any{}
		for i := 1 + r.IntN(5); i > 0; i-- {
			tree[tricky[r.IntN(len(tricky))]] = randomTree(r, 0)
		}
		out, err := Emit(tree)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		back, probs := Parse(out)
		if len(probs) > 0 {
			t.Fatalf("seed %d: emitted YAML does not parse: %v\n%s", seed, probs, out)
		}
		if !spec.Equal(tree, back) {
			t.Fatalf("seed %d: round trip changed the tree\n%s", seed, out)
		}
		again, _ := Emit(back)
		if !bytes.Equal(out, again) {
			t.Fatalf("seed %d: canonical YAML is not stable\n%s\n---\n%s", seed, out, again)
		}
	}
}

func TestEmitShape(t *testing.T) {
	tree := map[string]any{
		"name": "shop",
		"components": map[string]any{
			"api": map[string]any{"kind": "web", "port": int64(9000), "env": map[string]any{}},
		},
		"list":  []any{map[string]any{"b": int64(1), "a": "x"}, "y", []any{}},
		"flags": true,
		"empty": "",
	}
	out, err := Emit(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"components:",
		"  api:",
		"    env: {}",
		"    kind: web",
		"    port: 9000",
		`empty: ""`,
		"flags: true",
		"list:",
		"  - a: x",
		"    b: 1",
		`  - "y"`,
		"  - []",
		"name: shop",
		"",
	}, "\n")
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}
