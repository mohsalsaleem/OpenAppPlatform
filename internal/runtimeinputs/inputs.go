// Package runtimeinputs resolves administrator-authorized runtime references.
// Values are held only in process memory and never returned as configuration.
package runtimeinputs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type Binding struct {
	Reference     string `json:"reference"`
	TargetID      string `json:"targetId"`
	ApplicationID string `json:"applicationId"`
	Component     string `json:"component"`
	Variable      string `json:"variable"`
	ValueEnv      string `json:"valueEnv"`
}
type entry struct {
	binding Binding
	value   string
}
type Inputs struct{ entries []entry }

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// Parse takes only non-secret grants. lookup is used once at startup so a process
// cannot change values between admission and execution. Dedicated OAP_SECRET_
// variables prevent references from reaching arbitrary platform credentials.
func Parse(raw []byte, lookup func(string) string) (*Inputs, error) {
	if len(raw) > 64<<10 {
		return nil, errors.New("runtime input bindings exceed limit")
	}
	var bindings []Binding
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&bindings); err != nil {
		return nil, errors.New("invalid runtime input bindings")
	}
	if d.Decode(new(any)) != io.EOF || len(bindings) > 128 {
		return nil, errors.New("invalid runtime input bindings")
	}
	out := &Inputs{}
	seen := map[Binding]bool{}
	for _, b := range bindings {
		if !slug.MatchString(b.Reference) || !slug.MatchString(b.TargetID) || !slug.MatchString(b.Component) || len(b.ApplicationID) != 32 || strings.Trim(b.ApplicationID, "0123456789abcdef") != "" || !identifier.MatchString(b.Variable) || !identifier.MatchString(b.ValueEnv) || !strings.HasPrefix(b.ValueEnv, "OAP_SECRET_") {
			return nil, errors.New("runtime input grants require exact scope and a dedicated OAP_SECRET_ variable")
		}
		key := b
		key.ValueEnv = ""
		if seen[key] {
			return nil, errors.New("duplicate runtime input grant")
		}
		seen[key] = true
		value := lookup(b.ValueEnv)
		if value != "" && (len(value) < 8 || len(value) > 4096 || strings.ContainsRune(value, 0)) {
			return nil, errors.New("runtime input values must contain 8 to 4096 bytes without NUL")
		}
		out.entries = append(out.entries, entry{b, value})
	}
	return out, nil
}
func (i *Inputs) Resolve(target, app string, c domain.Component) (domain.Component, error) {
	env := map[string]string{}
	for k, v := range c.Env {
		env[k] = v
	}
	for key, ref := range c.SecretRefs {
		found := false
		if i != nil {
			for _, e := range i.entries {
				b := e.binding
				if b.TargetID == target && b.ApplicationID == app && b.Component == c.Name && b.Variable == key && b.Reference == ref && e.value != "" {
					env[key] = e.value
					found = true
					break
				}
			}
		}
		if !found {
			return domain.Component{}, errors.New("runtime secret reference is missing or not authorized for this application component")
		}
	}
	bytes := 0
	for k, v := range env {
		bytes += len(k) + len(v)
	}
	if bytes > 16<<10 {
		return domain.Component{}, errors.New("resolved runtime configuration exceeds 16 KiB")
	}
	c.Env = env
	return c, nil
}

// Fingerprint is private recovery metadata, never part of public definitions.
func Fingerprint(c domain.Component, nonce string) string {
	if len(c.SecretRefs) == 0 {
		return ""
	}
	values := map[string]string{}
	for k, ref := range c.SecretRefs {
		values[k] = ref + "\x00" + c.Env[k]
	}
	raw, _ := json.Marshal(struct {
		Nonce  string
		Values map[string]string
	}{nonce, values})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func (i *Inputs) Redact(text string) string {
	if i == nil {
		return text
	}
	values := []string{}
	for _, e := range i.entries {
		if e.value != "" {
			values = append(values, e.value)
		}
	}
	sort.Slice(values, func(a, b int) bool { return len(values[a]) > len(values[b]) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[REDACTED]")
	}
	return text
}
