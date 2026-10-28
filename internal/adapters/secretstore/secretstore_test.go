package secretstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
)

const value = "postgres://shop:s3cr3t-Value-91@db:5432/shop"

func newStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	key, path := filepath.Join(dir, "key"), filepath.Join(dir, "secrets.json")
	if err := CreateKey(key); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	return s, path, key
}

func TestContract(t *testing.T) {
	s, _, _ := newStore(t)
	s.Set("db-url", value)
	contract.Secrets(t, s)
}

func TestRoundTripAndReopen(t *testing.T) {
	s, path, key := newStore(t)
	if err := s.Set("db-url", value); err != nil {
		t.Fatal(err)
	}
	s.Set("api-key", "k-1")
	s2, err := Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Resolve(context.Background(), map[string]string{"DATABASE_URL": "db-url", "API_KEY": "api-key"})
	if err != nil || got["DATABASE_URL"] != value || got["API_KEY"] != "k-1" {
		t.Fatalf("%v %v", got, err)
	}
	if names, _ := s2.Names(); strings.Join(names, ",") != "api-key,db-url" {
		t.Fatalf("names %v", names)
	}
	s2.Delete("api-key")
	if _, err := s2.Resolve(context.Background(), map[string]string{"API_KEY": "api-key"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted secret: %v", err)
	}
	for _, p := range []string{path, key} {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", p, st.Mode())
		}
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatal("plaintext in the store file")
	}
}

func TestWrongKeyAndTamperingFailClosed(t *testing.T) {
	s, path, _ := newStore(t)
	s.Set("db-url", value)
	s.Set("other", "x")
	other := filepath.Join(t.TempDir(), "other-key")
	CreateKey(other)
	wrong, err := Open(path, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Resolve(context.Background(), map[string]string{"X": "db-url"}); err == nil {
		t.Fatal("a wrong key must not decrypt")
	}
	// Moving a value to another name breaks its authentication.
	var f file
	raw, _ := os.ReadFile(path)
	json.Unmarshal(raw, &f)
	f.Secrets["other"] = f.Secrets["db-url"]
	b, _ := json.Marshal(f)
	os.WriteFile(path, b, 0o600)
	if _, err := s.Resolve(context.Background(), map[string]string{"X": "other"}); err == nil {
		t.Fatal("a value moved to another name must not decrypt")
	}
	os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := Open(path, filepath.Join(filepath.Dir(path), "key")); err == nil {
		t.Fatal("a damaged store must not open")
	}
	if err := CreateKey(other); err == nil {
		t.Fatal("an existing key must not be overwritten")
	}
}

// TestErrorsNeverCarryValues collects every error the store can return
// and checks none contains a secret value.
func TestErrorsNeverCarryValues(t *testing.T) {
	s, path, _ := newStore(t)
	s.Set("db-url", value)
	var errs []error
	_, err := s.Resolve(context.Background(), map[string]string{"X": "missing"})
	errs = append(errs, err)
	errs = append(errs, s.Set("bad name!", value))
	other := filepath.Join(t.TempDir(), "k")
	CreateKey(other)
	wrong, _ := Open(path, other)
	_, err = wrong.Resolve(context.Background(), map[string]string{"X": "db-url"})
	errs = append(errs, err)
	for _, e := range errs {
		if e == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(e.Error(), "s3cr3t") {
			t.Fatalf("error carries a value: %v", e)
		}
	}
}
