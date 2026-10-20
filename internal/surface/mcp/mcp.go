// Package mcp serves the agent API as Model Context Protocol tools, over
// stdio or HTTP (JSON-RPC 2.0). The tools and their input schemas are
// generated from AgentService's protobuf descriptors: one tool per method,
// named after it in snake case, taking the request message as JSON with
// OAP's wire conventions and returning the response the same way.
// Streaming methods return their events as a list: await until the
// outcome, watch until it has nothing new for a moment.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/wire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// ProtocolVersion is the MCP version this server speaks.
const ProtocolVersion = "2025-06-18"

// descriptions are the tools' one-line summaries; the generated Go
// descriptors don't keep the proto's comments.
var descriptions = map[string]string{
	"Observe": "Return the world model, or one application, at the current revision.",
	"Watch":   "Return the events after a revision: proposals, applies, step progress, outcomes.",
	"Propose": "Compile an intent into a policy-checked plan. Changes nothing.",
	"Apply":   "Run exactly the proposal with this id. Idempotent.",
	"Await":   "Return an operation's progress and its outcome.",
	"Explain": "Explain why an application's actual state isn't its desired state.",
	"Ask":     "Involve the owner; the answer arrives through watch.",
	"Login":   "Start a device login.",
}

// Server is an MCP server over the service.
type Server struct {
	Service *service.Service
	Caller  service.Caller
	// WatchIdle is how long watch waits for a new event before returning.
	WatchIdle time.Duration
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	method      protoreflect.MethodDescriptor
}

var (
	toolsOnce sync.Once
	tools     []tool
)

func toolName(m protoreflect.MethodDescriptor) string {
	var b strings.Builder
	for i, r := range string(m.Name()) {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Tools returns the tools, generated from AgentService.
func Tools() []tool {
	toolsOnce.Do(func() {
		ms := oapv1.File_oap_v1_agent_proto.Services().ByName("AgentService").Methods()
		for i := 0; i < ms.Len(); i++ {
			m := ms.Get(i)
			tools = append(tools, tool{Name: toolName(m), Description: descriptions[string(m.Name())],
				InputSchema: Schema(m.Input()), method: m})
		}
	})
	return tools
}

// Schema returns a JSON Schema for a message in OAP's JSON form.
func Schema(md protoreflect.MessageDescriptor) map[string]any {
	defs := map[string]any{}
	root := messageSchema(md, defs, map[protoreflect.FullName]bool{})
	if len(defs) > 0 {
		root["$defs"] = defs
	}
	return root
}

func messageSchema(md protoreflect.MessageDescriptor, defs map[string]any, building map[protoreflect.FullName]bool) map[string]any {
	switch md.FullName() {
	case "google.protobuf.Struct":
		return map[string]any{"type": "object"}
	case "google.protobuf.Value":
		return map[string]any{}
	case "google.protobuf.Timestamp":
		return map[string]any{"type": "string", "format": "date-time"}
	}
	props := map[string]any{}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		var s map[string]any
		switch {
		case f.IsMap():
			s = map[string]any{"type": "object", "additionalProperties": fieldSchema(f.MapValue(), defs, building)}
		case f.IsList():
			s = map[string]any{"type": "array", "items": fieldSchema(f, defs, building)}
		default:
			s = fieldSchema(f, defs, building)
		}
		props[string(f.Name())] = s
	}
	return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
}

func fieldSchema(f protoreflect.FieldDescriptor, defs map[string]any, building map[protoreflect.FullName]bool) map[string]any {
	switch f.Kind() {
	case protoreflect.StringKind:
		return map[string]any{"type": "string"}
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return map[string]any{"type": "integer"}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		// 64-bit integers are decimal strings in OAP's JSON; numbers are accepted.
		return map[string]any{"type": []any{"string", "integer"}}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return map[string]any{"type": "number"}
	case protoreflect.BytesKind:
		return map[string]any{"type": "string", "contentEncoding": "base64"}
	case protoreflect.EnumKind:
		var names []any
		vals := f.Enum().Values()
		for i := 0; i < vals.Len(); i++ {
			names = append(names, string(vals.Get(i).Name()))
		}
		return map[string]any{"type": "string", "enum": names}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		md := f.Message()
		switch md.FullName() {
		case "google.protobuf.Struct", "google.protobuf.Value", "google.protobuf.Timestamp":
			return messageSchema(md, defs, building)
		}
		name := string(md.FullName())
		if _, done := defs[name]; !done && !building[md.FullName()] {
			building[md.FullName()] = true
			defs[name] = messageSchema(md, defs, building)
			delete(building, md.FullName())
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	return map[string]any{}
}

// JSON-RPC 2.0 messages.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Handle answers one JSON-RPC message; it returns nil for notifications.
func (s *Server) Handle(ctx context.Context, raw []byte) []byte {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil || req.JSONRPC != "2.0" {
		return mustJSON(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
	}
	if len(req.ID) == 0 {
		return nil // a notification, such as notifications/initialized
	}
	res := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		res.Result = map[string]any{"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]any{"name": "oap", "version": "1"},
			"instructions": "OAP runs applications on the owner's server. Observe first, propose a change, apply the proposal, then await its outcome."}
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		res.Result = map[string]any{"tools": Tools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			res.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		res.Result = s.call(ctx, p.Name, p.Arguments)
	default:
		res.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return mustJSON(res)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// toolResult is a tools/call result: the response as JSON text and as
// structured content, or an error carrying the ErrorDetail.
func toolResult(v any, isError bool) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}},
		"structuredContent": json.RawMessage(b), "isError": isError}
}

func errorResult(err error) map[string]any {
	var se *service.Error
	if errors.As(err, &se) {
		b, _ := wire.Marshal(se.Detail)
		return toolResult(json.RawMessage(b), true)
	}
	return toolResult(map[string]any{"why": "mcp.error", "message": err.Error()}, true)
}

func (s *Server) call(ctx context.Context, name string, args json.RawMessage) map[string]any {
	var t *tool
	for i := range Tools() {
		if tools[i].Name == name {
			t = &tools[i]
		}
	}
	if t == nil {
		return errorResult(fmt.Errorf("no tool %q", name))
	}
	in := dynamicpb.NewMessage(t.method.Input())
	if len(args) == 0 || string(args) == "null" {
		args = []byte("{}")
	}
	if err := wire.Unmarshal(args, in); err != nil {
		return errorResult(fmt.Errorf("arguments: %v", err))
	}
	out, err := s.dispatch(ctx, string(t.method.Name()), in)
	if err != nil {
		return errorResult(err)
	}
	b, err := marshalAny(out)
	if err != nil {
		return errorResult(err)
	}
	return toolResult(json.RawMessage(b), false)
}

// marshalAny writes a message, or a list of events, in wire JSON.
func marshalAny(v any) ([]byte, error) {
	switch v := v.(type) {
	case proto.Message:
		return wire.Marshal(v)
	case []*oapv1.Event:
		parts := make([]json.RawMessage, len(v))
		for i, e := range v {
			b, err := wire.Marshal(e)
			if err != nil {
				return nil, err
			}
			parts[i] = b
		}
		return json.Marshal(map[string]any{"events": parts})
	}
	return nil, fmt.Errorf("mcp: can't encode %T", v)
}

// convert copies a dynamic message into a typed one.
func convert[T proto.Message](in proto.Message, out T) (T, error) {
	b, err := proto.Marshal(in)
	if err != nil {
		return out, err
	}
	return out, proto.Unmarshal(b, out)
}

func (s *Server) dispatch(ctx context.Context, method string, in proto.Message) (any, error) {
	switch method {
	case "Observe":
		req, err := convert(in, &oapv1.ObserveRequest{})
		if err != nil {
			return nil, err
		}
		return s.Service.Observe(ctx, s.Caller, req)
	case "Propose":
		req, err := convert(in, &oapv1.ProposeRequest{})
		if err != nil {
			return nil, err
		}
		return s.Service.Propose(ctx, s.Caller, req)
	case "Apply":
		req, err := convert(in, &oapv1.ApplyRequest{})
		if err != nil {
			return nil, err
		}
		return s.Service.Apply(ctx, s.Caller, req)
	case "Explain":
		req, err := convert(in, &oapv1.ExplainRequest{})
		if err != nil {
			return nil, err
		}
		return s.Service.Explain(ctx, s.Caller, req)
	case "Ask":
		req, err := convert(in, &oapv1.AskRequest{})
		if err != nil {
			return nil, err
		}
		return s.Service.Ask(ctx, s.Caller, req)
	case "Await":
		req, err := convert(in, &oapv1.AwaitRequest{})
		if err != nil {
			return nil, err
		}
		var evs []*oapv1.Event
		err = s.Service.Await(ctx, s.Caller, req, func(e *oapv1.Event) error { evs = append(evs, e); return nil })
		return evs, err
	case "Watch":
		req, err := convert(in, &oapv1.WatchRequest{})
		if err != nil {
			return nil, err
		}
		return s.watch(ctx, req)
	case "Login":
		req, err := convert(in, &oapv1.LoginRequest{})
		if err != nil {
			return nil, err
		}
		return nil, s.Service.Login(ctx, req, func(*oapv1.LoginResponse) error { return nil })
	}
	return nil, fmt.Errorf("mcp: method %s isn't served", method)
}

var errIdle = errors.New("mcp: idle")

// watch collects events until none arrives for WatchIdle.
func (s *Server) watch(ctx context.Context, req *oapv1.WatchRequest) ([]*oapv1.Event, error) {
	idle := s.WatchIdle
	if idle <= 0 {
		idle = 2 * time.Second
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var evs []*oapv1.Event
	last := time.Now()
	go func() {
		t := time.NewTicker(idle / 4)
		defer t.Stop()
		for {
			select {
			case <-cctx.Done():
				return
			case <-t.C:
				mu.Lock()
				quiet := time.Since(last) >= idle
				mu.Unlock()
				if quiet {
					cancel()
					return
				}
			}
		}
	}()
	err := s.Service.Watch(cctx, s.Caller, req, func(e *oapv1.Event) error {
		mu.Lock()
		defer mu.Unlock()
		evs = append(evs, e)
		last = time.Now()
		return nil
	})
	if err != nil && ctx.Err() == nil && errors.Is(err, context.Canceled) {
		err = nil // stopped because it was quiet
	}
	mu.Lock()
	defer mu.Unlock()
	return evs, err
}

// ServeStdio reads newline-delimited JSON-RPC from r and writes responses
// to w, one per line.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if out := s.Handle(ctx, line); out != nil {
			if _, err := w.Write(append(out, '\n')); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// ServeHTTP serves MCP's streamable HTTP transport for requests that
// return one JSON response: POST a JSON-RPC message, get its response.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "MCP takes POST", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := s.Handle(r.Context(), body)
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}
