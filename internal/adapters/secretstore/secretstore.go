// Package secretstore keeps application secrets in an encrypted file
// (docs/architecture/agent-native-rewrite.md, section 9). Each value is
// sealed with AES-256-GCM under a key held in a separate file, with the
// secret's name as additional data, so a value can't be moved to another
// name. Errors name secrets, never their values, and a wrong key or a
// damaged file fails closed.
package secretstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

// ErrNotFound is returned for a secret reference the store doesn't hold.
var ErrNotFound = errors.New("secretstore: no such secret")

var secretName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type sealed struct {
	Nonce string `json:"nonce"`
	Data  string `json:"data"`
}

type file struct {
	Version int               `json:"version"`
	Secrets map[string]sealed `json:"secrets"`
}

// Store is an encrypted secret store.
type Store struct {
	path string
	aead cipher.AEAD
	mu   sync.Mutex
}

// CreateKey writes a new random 256-bit key to path (0600), refusing to
// overwrite an existing key.
func CreateKey(path string) error {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("secretstore: can't create the key: %w", err)
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Open opens the store at path with the key in keyPath. A missing store
// file is an empty store; it is created on the first Set.
func Open(path, keyPath string) (*Store, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("secretstore: can't read the key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(string(trimSpace(raw)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("secretstore: the key file doesn't hold a 256-bit key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, aead: aead}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

func (s *Store) load() (*file, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &file{Version: 1, Secrets: map[string]sealed{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil || f.Version != 1 {
		return nil, errors.New("secretstore: the store file is damaged or from an unknown version")
	}
	if f.Secrets == nil {
		f.Secrets = map[string]sealed{}
	}
	return &f, nil
}

func (s *Store) save(f *file) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".secrets-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Set stores a secret under name, replacing any earlier value.
func (s *Store) Set(name, value string) error {
	if !secretName.MatchString(name) {
		return fmt.Errorf("secretstore: %q isn't a secret name", name)
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	data := s.aead.Seal(nil, nonce, []byte(value), []byte(name))
	f.Secrets[name] = sealed{Nonce: base64.StdEncoding.EncodeToString(nonce), Data: base64.StdEncoding.EncodeToString(data)}
	return s.save(f)
}

// Delete removes a secret; removing a missing one succeeds.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := f.Secrets[name]; !ok {
		return nil
	}
	delete(f.Secrets, name)
	return s.save(f)
}

// Names lists the secrets' names, sorted. Values are never listed.
func (s *Store) Names() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Secrets))
	for n := range f.Secrets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// Resolve maps environment variable names to secret values, from refs of
// variable name to secret name. It implements ports.Secrets.
func (s *Store) Resolve(ctx context.Context, refs map[string]string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(refs))
	for env, name := range refs {
		e, ok := f.Secrets[name]
		if !ok {
			return nil, fmt.Errorf("%w: %q (for %s)", ErrNotFound, name, env)
		}
		nonce, err1 := base64.StdEncoding.DecodeString(e.Nonce)
		data, err2 := base64.StdEncoding.DecodeString(e.Data)
		if err1 != nil || err2 != nil || len(nonce) != s.aead.NonceSize() {
			return nil, fmt.Errorf("secretstore: secret %q is damaged", name)
		}
		plain, err := s.aead.Open(nil, nonce, data, []byte(name))
		if err != nil {
			return nil, fmt.Errorf("secretstore: can't decrypt secret %q: wrong key or damaged store", name)
		}
		out[env] = string(plain)
	}
	return out, nil
}
