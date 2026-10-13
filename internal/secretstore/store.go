// Package secretstore provides an opt-in, host-managed AES-GCM file store.
package secretstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxFile = 2 << 20

var ErrUnavailable = errors.New("secret store or key is unavailable, invalid, or cannot be authenticated")

type Record struct {
	Reference     string `json:"reference"`
	TargetID      string `json:"targetId"`
	ApplicationID string `json:"applicationId"`
	Component     string `json:"component"`
	Variable      string `json:"variable"`
	Value         string `json:"value"`
}
type Grant struct {
	Reference     string `json:"reference"`
	TargetID      string `json:"targetId"`
	ApplicationID string `json:"applicationId"`
	Component     string `json:"component"`
	Variable      string `json:"variable"`
}
type Status struct {
	Enabled  bool       `json:"enabled"`
	State    string     `json:"state"`
	LoadedAt *time.Time `json:"loadedAt,omitempty"`
	Grants   []Grant    `json:"grants"`
}
type envelope struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func privateRead(path string, limit int) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > int64(limit) {
		return nil, ErrUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit+1)))
	if err != nil || len(raw) > limit {
		return nil, ErrUnavailable
	}
	return raw, nil
}
func key(path string) (cipher.AEAD, error) {
	raw, err := privateRead(path, 32)
	if err != nil || len(raw) != 32 {
		return nil, ErrUnavailable
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, ErrUnavailable
	}
	return cipher.NewGCM(block)
}
func decode(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrUnavailable
	}
	return nil
}
func Load(path, keyPath string) ([]Record, error) {
	aead, err := key(keyPath)
	if err != nil {
		return nil, err
	}
	raw, err := privateRead(path, maxFile)
	if err != nil {
		return nil, err
	}
	var data envelope
	if decode(raw, &data) != nil || data.Version != 1 || len(data.Nonce) != aead.NonceSize() {
		return nil, ErrUnavailable
	}
	plain, err := aead.Open(nil, data.Nonce, data.Ciphertext, []byte("OpenAppPlatform secret store v1"))
	if err != nil {
		return nil, ErrUnavailable
	}
	var records []Record
	if decode(plain, &records) != nil || len(records) > 128 {
		return nil, ErrUnavailable
	}
	if _, err = Inputs(nil, records); err != nil {
		return nil, ErrUnavailable
	}
	return records, nil
}
func Inputs(existing *runtimeinputs.Inputs, records []Record) (*runtimeinputs.Inputs, error) {
	values := make([]runtimeinputs.StoredInput, 0, len(records))
	for _, r := range records {
		values = append(values, runtimeinputs.StoredInput{Binding: runtimeinputs.Binding{Reference: r.Reference, TargetID: r.TargetID, ApplicationID: r.ApplicationID, Component: r.Component, Variable: r.Variable}, Value: r.Value})
	}
	return runtimeinputs.AddStored(existing, values)
}
func Snapshot(path, keyPath string) ([]Record, Status) {
	status := Status{Enabled: path != "" || keyPath != "", State: "disabled", Grants: []Grant{}}
	if !status.Enabled {
		return nil, status
	}
	status.State = "unavailable"
	records, err := Load(path, keyPath)
	if err != nil {
		return nil, status
	}
	now := time.Now().UTC()
	status.State = "ready"
	status.LoadedAt = &now
	for _, r := range records {
		status.Grants = append(status.Grants, Grant{r.Reference, r.TargetID, r.ApplicationID, r.Component, r.Variable})
	}
	return records, status
}
func write(path string, raw []byte, replace bool) error {
	if !filepath.IsAbs(path) || len(raw) > maxFile {
		return ErrUnavailable
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrUnavailable
	}
	if !replace {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return ErrUnavailable
		}
		defer file.Close()
		if _, err = file.Write(raw); err != nil {
			return ErrUnavailable
		}
		if file.Sync() != nil {
			return ErrUnavailable
		}
		if file.Close() != nil {
			return ErrUnavailable
		}
		return syncDirectory(path)
	}
	file, err := os.CreateTemp(dir, ".oap-secret-*")
	if err != nil {
		return ErrUnavailable
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if file.Chmod(0600) != nil {
		file.Close()
		return ErrUnavailable
	}
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return ErrUnavailable
	}
	if file.Sync() != nil {
		file.Close()
		return ErrUnavailable
	}
	if file.Close() != nil {
		return ErrUnavailable
	}
	if os.Rename(temporary, path) != nil {
		return ErrUnavailable
	}
	return syncDirectory(path)
}
func save(path, keyPath string, records []Record, replace bool) error {
	if _, err := Inputs(nil, records); err != nil {
		return ErrUnavailable
	}
	plain, err := json.Marshal(records)
	if err != nil || len(plain) > 1<<20 {
		return ErrUnavailable
	}
	aead, err := key(keyPath)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return ErrUnavailable
	}
	raw, _ := json.Marshal(envelope{Version: 1, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, []byte("OpenAppPlatform secret store v1"))})
	return write(path, raw, replace)
}

// Init preserves an owner-supplied key, or generates a new one without printing it.
func Init(path, keyPath string) error {
	if path == keyPath || !filepath.IsAbs(path) || !filepath.IsAbs(keyPath) {
		return ErrUnavailable
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return ErrUnavailable
	}
	if _, err := os.Lstat(keyPath); os.IsNotExist(err) {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return ErrUnavailable
		}
		if err = write(keyPath, raw, false); err != nil {
			return err
		}
	}
	return save(path, keyPath, []Record{}, false)
}

// Put serializes host writes with an exclusive lock. A crashed writer leaves a
// lock requiring host inspection; no automatic stale-lock deletion occurs.
func Put(path, keyPath string, record Record) error {
	if !filepath.IsAbs(path) {
		return ErrUnavailable
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("secret store is locked; inspect the host before retrying")
	}
	lock.Close()
	defer os.Remove(path + ".lock")
	records, err := Load(path, keyPath)
	if err != nil {
		return err
	}
	found := false
	for index, r := range records {
		if r.Reference == record.Reference && r.TargetID == record.TargetID && r.ApplicationID == record.ApplicationID && r.Component == record.Component && r.Variable == record.Variable {
			records[index] = record
			found = true
			break
		}
	}
	if !found {
		records = append(records, record)
	}
	return save(path, keyPath, records, true)
}
func ReadRecord(reader io.Reader) (Record, error) {
	var r Record
	raw, err := io.ReadAll(io.LimitReader(reader, 8193))
	if err != nil || len(raw) > 8192 || decode(raw, &r) != nil {
		return r, errors.New("provide one bounded scoped secret JSON document on stdin")
	}
	if strings.ContainsRune(r.Value, 0) {
		return Record{}, ErrUnavailable
	}
	return r, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return ErrUnavailable
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}
