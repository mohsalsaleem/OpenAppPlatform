package service

import (
	"strings"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
)

// TestOwnerSecrets: only the owner sets, lists and deletes secrets; names
// follow the spec's rule; a value set for a secret ask must match its
// reference and answers it; the journal records names, never values.
func TestOwnerSecrets(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.ListSecrets(ctx, Owner); code(err) != FailedPrecondition {
		t.Fatalf("without a store: %v", err)
	}
	store := sim.Secrets{}
	r.svc.SecretStore = store
	const value = "s3cret-value-never-journaled"

	agent := Caller{Agent: "claude", Apps: []string{"site"}}
	if _, err := r.svc.SetSecret(ctx, agent, &oapv1.SetSecretRequest{Name: "db", Value: value}); code(err) != PermissionDenied {
		t.Fatalf("agent set a secret: %v", err)
	}
	if _, err := r.svc.ListSecrets(ctx, agent); code(err) != PermissionDenied {
		t.Fatalf("agent listed secrets: %v", err)
	}
	for _, bad := range []*oapv1.SetSecretRequest{{Name: "DB", Value: "x"}, {Name: "db/x", Value: "x"}, {Name: "db", Value: ""},
		{Name: "db", Value: strings.Repeat("x", MaxSecretSize+1)}} {
		if _, err := r.svc.SetSecret(ctx, Owner, bad); code(err) != InvalidArgument {
			t.Errorf("accepted %q (%d bytes): %v", bad.Name, len(bad.Value), err)
		}
	}

	ask, err := r.svc.Ask(ctx, agent, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_SECRET, SecretRef: "db", Question: "db password?"})
	if err != nil {
		t.Fatal(err)
	}
	blocker, _ := r.svc.Ask(ctx, agent, &oapv1.AskRequest{Kind: oapv1.AskKind_ASK_KIND_BLOCKER, Question: "?"})
	if _, err := r.svc.AnswerAsk(ctx, Owner, &oapv1.AnswerAskRequest{AskId: ask.AskId, Answer: "provided"}); code(err) != FailedPrecondition {
		t.Fatalf("provided before the secret exists: %v", err)
	}
	if _, err := r.svc.SetSecret(ctx, Owner, &oapv1.SetSecretRequest{Name: "other", Value: value, AskId: ask.AskId}); code(err) != InvalidArgument {
		t.Fatalf("value for another secret answered the ask: %v", err)
	}
	if _, err := r.svc.SetSecret(ctx, Owner, &oapv1.SetSecretRequest{Name: "db", Value: value, AskId: blocker.AskId}); code(err) != InvalidArgument {
		t.Fatalf("a blocker answered with a secret: %v", err)
	}
	if _, ok := store["other"]; ok {
		t.Fatal("a refused request stored its value")
	}
	if _, err := r.svc.SetSecret(ctx, Owner, &oapv1.SetSecretRequest{Name: "db", Value: value, AskId: ask.AskId}); err != nil {
		t.Fatal(err)
	}
	if store["db"] != value {
		t.Fatal("not stored")
	}
	asks, _ := r.svc.ListAsks(ctx, Owner)
	for _, a := range asks.Asks {
		if a.AskId == ask.AskId {
			t.Fatal("the secret ask is still open")
		}
	}
	names, err := r.svc.ListSecrets(ctx, Owner)
	if err != nil || len(names.Names) != 1 || names.Names[0] != "db" {
		t.Fatalf("names: %v %v", names, err)
	}
	if _, err := r.svc.DeleteSecret(ctx, Owner, &oapv1.DeleteSecretRequest{Name: "nope"}); code(err) != NotFound {
		t.Fatalf("delete unknown: %v", err)
	}
	if _, err := r.svc.DeleteSecret(ctx, Owner, &oapv1.DeleteSecretRequest{Name: "db"}); err != nil || len(store) != 0 {
		t.Fatalf("delete: %v %v", err, store)
	}

	entries, _ := r.svc.entries(ctx, 0)
	var journal strings.Builder
	sets := 0
	for _, e := range entries {
		journal.Write(e.Data)
		if e.Kind == KindSecret {
			sets++
		}
	}
	if strings.Contains(journal.String(), value) {
		t.Fatal("the journal holds a secret value")
	}
	if sets != 2 {
		t.Fatalf("want the set and the delete journaled, got %d entries", sets)
	}
}
