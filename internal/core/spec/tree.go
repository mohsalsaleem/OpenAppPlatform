// Package spec defines OAP's desired-state format: the data tree every
// encoding (YAML, JSON) decodes into, its JSON Schema, the canonical form and
// content hash, and the typed application spec with its semantic rules.
//
// A tree holds only these values: map[string]any, []any, string, int64 and
// bool. There are no floats and no nulls.
package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// FromJSON decodes JSON into a tree, rejecting floats, nulls and duplicate keys.
func FromJSON(b []byte) (any, []Problem) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	v, probs := readJSON(dec, "")
	if len(probs) > 0 {
		return nil, probs
	}
	if _, err := dec.Token(); err == nil {
		return nil, []Problem{{Why: WhyJSONSyntax, Message: "unexpected data after the document"}}
	}
	return v, nil
}

func readJSON(dec *json.Decoder, path string) (any, []Problem) {
	tok, err := dec.Token()
	if err != nil {
		return nil, []Problem{{Path: path, Why: WhyJSONSyntax, Message: err.Error()}}
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, []Problem{{Path: path, Why: WhyJSONSyntax, Message: err.Error()}}
				}
				key := kt.(string)
				child := Join(path, key)
				if _, dup := m[key]; dup {
					return nil, []Problem{{Path: child, Why: WhyDuplicateKey, Message: "key appears more than once"}}
				}
				v, probs := readJSON(dec, child)
				if len(probs) > 0 {
					return nil, probs
				}
				m[key] = v
			}
			_, _ = dec.Token()
			return m, nil
		case '[':
			l := []any{}
			for i := 0; dec.More(); i++ {
				v, probs := readJSON(dec, Index(path, i))
				if len(probs) > 0 {
					return nil, probs
				}
				l = append(l, v)
			}
			_, _ = dec.Token()
			return l, nil
		}
	case json.Number:
		n, err := strconv.ParseInt(t.String(), 10, 64)
		if err != nil {
			return nil, []Problem{{Path: path, Why: WhyFloat, Message: "numbers must be integers: " + t.String()}}
		}
		return n, nil
	case string, bool:
		return t, nil
	case nil:
		return nil, []Problem{{Path: path, Why: WhyNull, Message: "null is not allowed; omit the field instead"}}
	}
	return nil, []Problem{{Path: path, Why: WhyJSONSyntax, Message: fmt.Sprintf("unexpected token %v", tok)}}
}

// Canonical returns the canonical JSON encoding of a tree: sorted keys, no
// insignificant whitespace, no HTML escaping.
func Canonical(tree any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case string:
		writeString(buf, t)
	case int64:
		buf.WriteString(strconv.FormatInt(t, 10))
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	default:
		return fmt.Errorf("spec: %T is not a tree value", v)
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	buf.Truncate(buf.Len() - 1) // Encode appends a newline
}

// Hash returns the content hash of a tree's canonical form, as "sha256:<hex>".
func Hash(tree any) (string, error) {
	b, err := Canonical(tree)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Equal reports whether two trees are identical.
func Equal(a, b any) bool {
	ca, errA := Canonical(a)
	cb, errB := Canonical(b)
	return errA == nil && errB == nil && bytes.Equal(ca, cb)
}

// Join appends a map key to a path, for example "components.api".
func Join(path, key string) string {
	if strings.ContainsAny(key, ".[]") {
		key = strconv.Quote(key)
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// Index appends a list index to a path, for example "routes[0]".
func Index(path string, i int) string {
	return path + "[" + strconv.Itoa(i) + "]"
}
