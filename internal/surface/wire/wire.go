// Package wire fixes how OAP messages look as JSON, for every interface that
// speaks JSON (Connect, MCP, the CLI's --json and --events). See
// proto/README.md for the conventions.
package wire

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	marshal   = protojson.MarshalOptions{UseProtoNames: true}
	unmarshal = protojson.UnmarshalOptions{}
)

// Marshal encodes m as compact JSON. Output is byte-stable for equal messages.
func Marshal(m proto.Message) ([]byte, error) {
	b, err := marshal.Marshal(m)
	if err != nil {
		return nil, err
	}
	// protojson varies whitespace on purpose; compacting makes output stable.
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Unmarshal decodes JSON into m. It accepts proto and lowerCamelCase field
// names, and 64-bit integers as numbers or strings. Unknown fields are errors.
func Unmarshal(b []byte, m proto.Message) error {
	return unmarshal.Unmarshal(b, m)
}
