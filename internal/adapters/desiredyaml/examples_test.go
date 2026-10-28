package desiredyaml

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

var update = flag.Bool("update", false, "rewrite golden files")

// notCarriedOver lists example specs that depend on adopting existing
// resources, which only the Coolify operator supported. Docker is the only
// operator in the rewrite, so these must fail with convert.unsupported.
var notCarriedOver = map[string]bool{
	"agents/portable/connected-application.json": true,
	"application-breadth/application.json":       true,
	"coolify-connected/application.json":         true,
}

// legacySpecs returns every application spec in v0.1's examples, kept in
// testdata/legacy when v0.1 was retired, keyed by path.
func legacySpecs(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("testdata", "legacy")
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var probe map[string]any
		if json.Unmarshal(b, &probe) != nil {
			return nil // not an object: target lists and the like
		}
		_, bare := probe["components"]
		_, wrapped := probe["spec"]
		if bare || (wrapped && probe["kind"] == "Application") {
			rel, _ := filepath.Rel(root, path)
			// Examples are templates; fill digest placeholders as a user would.
			b = bytes.ReplaceAll(b, []byte("sha256:REPLACE_WITH_DIGEST"), []byte("sha256:"+strings.Repeat("0", 64)))
			out[filepath.ToSlash(rel)] = b
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 15 {
		t.Fatalf("found only %d example specs", len(out))
	}
	return out
}

func TestEveryExampleConvertsOrIsExplicitlyDropped(t *testing.T) {
	specs := legacySpecs(t)
	names := make([]string, 0, len(specs))
	for n := range specs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			tree, _, problems := spec.ConvertLegacy(specs[name])
			if notCarriedOver[name] {
				if len(problems) == 0 {
					t.Fatal("expected convert.unsupported; remove it from notCarriedOver if it now converts")
				}
				for _, p := range problems {
					if p.Why != spec.WhyLegacyUnsupported {
						t.Fatalf("want only %s, got %v", spec.WhyLegacyUnsupported, p)
					}
				}
				return
			}
			if len(problems) > 0 {
				t.Fatalf("conversion problems: %v", problems)
			}
			out, err := Emit(tree)
			if err != nil {
				t.Fatal(err)
			}
			back, probs := Parse(out)
			if len(probs) > 0 || !spec.Equal(tree, back) {
				t.Fatalf("converted YAML does not round-trip: %v\n%s", probs, out)
			}
			if _, probs := spec.ParseApplication(back); len(probs) > 0 {
				t.Fatalf("converted spec is invalid: %v\n%s", probs, out)
			}
			golden := filepath.Join("testdata", "examples", strings.TrimSuffix(strings.ReplaceAll(name, "/", "--"), ".json")+".yaml")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("converted YAML differs from %s:\n%s", golden, out)
			}
		})
	}
}

func TestConvertLegacyReportsDroppedTarget(t *testing.T) {
	_, notes, problems := spec.ConvertLegacy([]byte(`{"name":"a","environment":"staging","targetId":"local-docker",
		"components":[{"name":"web","image":"nginx","port":80}]}`))
	if len(problems) > 0 || len(notes) != 1 || notes[0].Path != "targetId" {
		t.Fatalf("notes %v problems %v", notes, problems)
	}
}

func TestConvertLegacyRejectsRedactedAndUnknownFields(t *testing.T) {
	for _, in := range []string{
		`{"apiVersion":"oap.dev/v1alpha1","kind":"Application","spec":{"name":"a","environment":"s","targetId":"t","components":[]},"redactedFields":{"web":["env.X"]}}`,
		`{"name":"a","environment":"s","targetId":"t","components":[{"name":"web","image":"x","port":1,"surprise":true}]}`,
		`{"apiVersion":"oap.dev/v2","kind":"Application","spec":{"name":"a"}}`,
	} {
		if _, _, problems := spec.ConvertLegacy([]byte(in)); len(problems) != 1 || problems[0].Why != spec.WhyLegacyFormat {
			t.Errorf("%s: got %v", in, problems)
		}
	}
}

func TestHandwrittenApplication(t *testing.T) {
	doc := `api_version: oap.dev/v1
components:
  web:
    health:
      mode: http
      path: /healthz
    image: ghcr.io/acme/web:1.4
    instances: 2
    kind: web
    port: 8080
environment: production
ingress:
  domain: shop.example.com
  https: true
  routes:
    /:
      component: web
kind: Application
name: shop
`
	tree, probs := Parse([]byte(doc))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	app, probs := spec.ParseApplication(tree)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	if app.Components["web"].Instances != 2 || !app.Ingress.HTTPS {
		t.Fatalf("%+v", app)
	}
	out, _ := Emit(tree)
	if string(out) != doc {
		t.Fatalf("canonical form differs from a canonically written document:\n%s", out)
	}
}

// The pre-deploy job pattern from v0.1's examples/pre-deploy-jobs/README.md: the API
// depends on a migration job. dependsOn is ordering, so it becomes after.
func TestConvertLegacyPreDeployJob(t *testing.T) {
	tree, _, problems := spec.ConvertLegacy([]byte(`{"name":"shop","environment":"staging","targetId":"local-docker",
		"components":[
		  {"name":"migrate","kind":"job","image":"registry.example/migrations@sha256:` + strings.Repeat("a", 64) + `",
		   "instances":1,"secretRefs":{"DATABASE_URL":"database-url"},"readiness":{"timeoutSeconds":120}},
		  {"name":"api","kind":"web","image":"registry.example/api:1","port":8080,"dependsOn":["migrate"]},
		  {"name":"web","kind":"web","image":"registry.example/web:1","port":8080,"dependsOn":["api"],"services":{"API_URL":"api"}}]}`))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	app, probs := spec.ParseApplication(tree)
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	api, web := app.Components["api"], app.Components["web"]
	if len(api.After) != 1 || api.After[0] != "migrate" || len(api.Uses) != 0 {
		t.Fatalf("api: %+v", api)
	}
	if len(web.After) != 0 || web.Uses["api"].Env != "API_URL" {
		t.Fatalf("web: %+v", web)
	}
}
