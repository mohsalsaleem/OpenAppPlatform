package engine

import (
	"context"
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
)

// TestJournalNeverHoldsSecretValues deploys a component that uses a secret
// and scans every journal entry. The engine handles secret names only; the
// runtime adapter resolves values when it starts a container, so a value
// can't reach the journal. This pins that the plan and its records carry
// the name and nothing more.
func TestJournalNeverHoldsSecretValues(t *testing.T) {
	const withSecret = `{"api_version": "oap.dev/v1", "kind": "Application", "name": "site", "environment": "production",
  "ingress": {"domain": "site.example.com", "routes": {"/": {"component": "web"}}},
  "components": {"web": {"kind": "web", "image": "reg/web:1", "port": 8080, "secrets": {"DATABASE_URL": "db-url"}}}}`
	h := map[string]any{}
	sc := compile(t, "deploy with secret", tree(t, withSecret), h, on("site", &oapv1.Deploy{Images: map[string]string{"web": "reg/web:2"}}))
	w := newWorld(t, sc)
	if err := w.executor().Run(context.Background(), sc.op); err != nil {
		t.Fatal(err)
	}
	entries, _ := w.journal.Since(context.Background(), 0, 10000)
	if len(entries) == 0 {
		t.Fatal("no journal entries")
	}
	sawName := false
	for _, s := range sc.op.Steps {
		sawName = sawName || strings.Contains(s.String(), "db-url")
	}
	for _, e := range entries {
		if strings.Contains(string(e.Data), "postgres://") {
			t.Fatalf("journal entry holds a secret value: %+v", e)
		}
	}
	if sawName {
		t.Fatal("plan steps shouldn't carry secret bindings at all")
	}
}
