package coolify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type variable struct {
	UUID      string `json:"uuid"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Multiline bool   `json:"is_multiline"`
	Literal   bool   `json:"is_literal"`
	Runtime   bool   `json:"is_runtime"`
	Build     bool   `json:"is_buildtime"`
	Preview   bool   `json:"is_preview"`
	Shared    bool   `json:"is_shared"`
	Hidden    bool   `json:"is_shown_once"`
}

func variableHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func safeVariable(v variable) bool {
	return v.UUID != "" && v.Literal && v.Runtime && v.Multiline == strings.Contains(v.Value, "\n") && !v.Build && !v.Preview && !v.Shared && !v.Hidden
}
func (c *Client) syncVariables(ctx context.Context, ref string, s operator.Spec) error {
	if s.Variables == nil {
		if len(s.Component.Env) > 0 {
			return errors.New("runtime variable ownership journal is required")
		}
		return nil
	}
	if e := s.Variables.Reserve(ctx, ref); e != nil {
		return e
	}
	owned, e := s.Variables.Variables(ctx, ref)
	if e != nil {
		return e
	}
	if len(owned) == 0 && len(s.Component.Env) == 0 {
		return nil
	}
	resource, e := c.Inspect(ctx, ref)
	if e != nil {
		return e
	}
	if resource.Description != s.Ownership {
		return errors.New("resource ownership changed before runtime configuration")
	}
	path := "/applications/" + url.PathEscape(ref) + "/envs"
	var rows []variable
	if e = c.request(ctx, "GET", path, nil, &rows); e != nil {
		return e
	}
	byKey := map[string]variable{}
	byID := map[string]variable{}
	for _, row := range rows {
		byID[row.UUID] = row
		if row.Preview {
			continue
		}
		if _, duplicate := byKey[row.Key]; duplicate {
			return errors.New("duplicate operator variable keys require review")
		}
		byKey[row.Key] = row
		byID[row.UUID] = row
	}
	// Validate all requested changes before making any variable mutation.
	for key, state := range owned {
		current, exists := byKey[key]
		if !exists {
			if _, renamed := byID[state.UUID]; renamed {
				return errors.New("managed operator variable was renamed; review required")
			}
			continue
		}
		if state.UUID == "" || current.UUID != state.UUID || !safeVariable(current) {
			return errors.New("operator variable ownership or flags changed; review required")
		}
		hash := variableHash(current.Value)
		if hash != state.Hash && (state.Intent == "" || hash != state.Intent) {
			return errors.New("managed operator variable changed outside OAP; review required")
		}
	}
	for key := range s.Component.Env {
		if _, exists := byKey[key]; exists {
			if _, known := owned[key]; !known {
				return errors.New("requested variable is managed by the operator; OAP will not overwrite it")
			}
		}
	}
	keys := []string{}
	for key := range s.Component.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := s.Component.Env[key]
		hash := variableHash(value)
		current, exists := byKey[key]
		state := owned[key]
		if !exists && state.UUID != "" {
			if e = s.Variables.Forget(ctx, ref, key, state.UUID); e != nil {
				return e
			}
			state = operator.VariableOwnership{}
		}
		if exists && variableHash(current.Value) == hash {
			if state.Intent != "" {
				if e = s.Variables.Begin(ctx, ref, key, current.UUID, hash); e != nil {
					return e
				}
				if e = s.Variables.Commit(ctx, ref, key, current.UUID, hash); e != nil {
					return e
				}
			}
			continue
		}
		uuid := ""
		method := "POST"
		if exists {
			uuid = current.UUID
			method = "PATCH"
		}
		if e = s.Variables.Begin(ctx, ref, key, uuid, hash); e != nil {
			return e
		}
		input := map[string]any{"key": key, "value": value, "is_literal": true, "is_runtime": true, "is_buildtime": false, "is_preview": false, "is_multiline": strings.Contains(value, "\n")}
		var result struct {
			UUID string `json:"uuid"`
		}
		if e = c.request(ctx, method, path, input, &result); e != nil {
			return errors.New("runtime variable update outcome is uncertain; inspect operator before retrying")
		}
		if result.UUID == "" {
			return errors.New("operator returned no variable identity; inspect operator")
		}
		if exists && result.UUID != uuid {
			return errors.New("operator variable identity changed during update")
		}
		if e = s.Variables.Commit(ctx, ref, key, result.UUID, hash); e != nil {
			return e
		}
	}
	for key, state := range owned {
		if _, wanted := s.Component.Env[key]; wanted {
			continue
		}
		if current, exists := byKey[key]; exists {
			if e = c.request(ctx, "DELETE", path+"/"+url.PathEscape(current.UUID), nil, nil); e != nil {
				return errors.New("runtime variable removal outcome is uncertain; inspect operator before retrying")
			}
		}
		var verified []variable
		if e = c.request(ctx, "GET", path, nil, &verified); e != nil {
			return e
		}
		for _, row := range verified {
			if row.UUID == state.UUID && state.UUID != "" {
				return errors.New("operator did not remove the managed variable")
			}
		}
		if e = s.Variables.Forget(ctx, ref, key, state.UUID); e != nil {
			return e
		}
	}
	var verified []variable
	if e = c.request(ctx, "GET", path, nil, &verified); e != nil {
		return e
	}
	latest, e := s.Variables.Variables(ctx, ref)
	if e != nil {
		return e
	}
	for key, value := range s.Component.Env {
		found := false
		for _, row := range verified {
			if row.Key == key && row.UUID == latest[key].UUID && !row.Preview && safeVariable(row) && variableHash(row.Value) == variableHash(value) {
				found = true
				break
			}
		}
		if !found {
			return errors.New("operator runtime configuration verification failed")
		}
	}
	return nil
}
