// Package agentapi serves the agent API over Connect, which speaks gRPC,
// gRPC-Web and Connect's JSON protocol on one handler. It translates
// between the wire and the service and holds no logic of its own; every
// error carries its why code and remedy as a typed detail.
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/wire"
	"google.golang.org/protobuf/proto"
)

// Server adapts the service to the generated handler interface.
type Server struct {
	Service *service.Service
	// Caller is who a request without a token comes from: the owner on
	// oapd's local socket. Nil on the public listener, where every request
	// but a login needs a token.
	Caller *service.Caller
}

// Bearer resolves a request's caller: an agent from its bearer token, or
// the fallback (the owner on the local socket) when there is none.
func Bearer(svc *service.Service, fallback *service.Caller) func(context.Context, http.Header) (service.Caller, error) {
	return func(ctx context.Context, h http.Header) (service.Caller, error) {
		if tok, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer "); ok {
			return svc.Authenticate(ctx, strings.TrimSpace(tok))
		}
		if fallback != nil {
			return *fallback, nil
		}
		return service.Caller{}, &service.Error{Code: service.Unauthenticated,
			Detail: &oapv1.ErrorDetail{Why: "identity.no_token", Message: "sign in with oapctl login and send the token"}}
	}
}

func (s *Server) who(ctx context.Context, h http.Header) (service.Caller, error) {
	return Bearer(s.Service, s.Caller)(ctx, h)
}

var _ oapv1connect.AgentServiceHandler = (*Server)(nil)

// Handler returns the HTTP path and handler for the agent API. JSON follows
// OAP's wire conventions (field names as written in the protos), not
// protojson's default lowerCamelCase.
func (s *Server) Handler() (string, http.Handler) {
	return oapv1connect.NewAgentServiceHandler(s, connect.WithCodec(JSONCodec{}))
}

// JSONCodec is Connect's "json" codec with OAP's JSON conventions.
type JSONCodec struct{}

func (JSONCodec) Name() string { return "json" }

func (JSONCodec) Marshal(v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("agentapi: %T isn't a protobuf message", v)
	}
	return wire.Marshal(m)
}

func (JSONCodec) Unmarshal(b []byte, v any) error {
	m, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("agentapi: %T isn't a protobuf message", v)
	}
	return wire.Unmarshal(b, m)
}

var codes = map[service.Code]connect.Code{
	service.InvalidArgument:    connect.CodeInvalidArgument,
	service.NotFound:           connect.CodeNotFound,
	service.FailedPrecondition: connect.CodeFailedPrecondition,
	service.PermissionDenied:   connect.CodePermissionDenied,
	service.Unimplemented:      connect.CodeUnimplemented,
	service.Internal:           connect.CodeInternal,
	service.Unauthenticated:    connect.CodeUnauthenticated,
}

// ToConnect turns a service error into a Connect error with the ErrorDetail.
func ToConnect(err error) error {
	if err == nil {
		return nil
	}
	var se *service.Error
	if !errors.As(err, &se) {
		if errors.Is(err, context.Canceled) {
			return connect.NewError(connect.CodeCanceled, err)
		}
		return connect.NewError(connect.CodeInternal, err)
	}
	ce := connect.NewError(codes[se.Code], errors.New(se.Detail.GetMessage()))
	if d, derr := connect.NewErrorDetail(se.Detail); derr == nil {
		ce.AddDetail(d)
	}
	return ce
}

func (s *Server) Observe(ctx context.Context, req *connect.Request[oapv1.ObserveRequest]) (*connect.Response[oapv1.ObserveResponse], error) {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return nil, ToConnect(err)
	}
	out, err := s.Service.Observe(ctx, c, req.Msg)
	if err != nil {
		return nil, ToConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Watch(ctx context.Context, req *connect.Request[oapv1.WatchRequest], stream *connect.ServerStream[oapv1.WatchResponse]) error {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return ToConnect(err)
	}
	return ToConnect(s.Service.Watch(ctx, c, req.Msg, func(e *oapv1.Event) error {
		return stream.Send(&oapv1.WatchResponse{Event: e})
	}))
}

func (s *Server) Propose(ctx context.Context, req *connect.Request[oapv1.ProposeRequest]) (*connect.Response[oapv1.ProposeResponse], error) {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return nil, ToConnect(err)
	}
	out, err := s.Service.Propose(ctx, c, req.Msg)
	if err != nil {
		return nil, ToConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Apply(ctx context.Context, req *connect.Request[oapv1.ApplyRequest]) (*connect.Response[oapv1.ApplyResponse], error) {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return nil, ToConnect(err)
	}
	out, err := s.Service.Apply(ctx, c, req.Msg)
	if err != nil {
		return nil, ToConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Await(ctx context.Context, req *connect.Request[oapv1.AwaitRequest], stream *connect.ServerStream[oapv1.AwaitResponse]) error {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return ToConnect(err)
	}
	return ToConnect(s.Service.Await(ctx, c, req.Msg, func(e *oapv1.Event) error {
		return stream.Send(&oapv1.AwaitResponse{Event: e})
	}))
}

func (s *Server) Explain(ctx context.Context, req *connect.Request[oapv1.ExplainRequest]) (*connect.Response[oapv1.ExplainResponse], error) {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return nil, ToConnect(err)
	}
	out, err := s.Service.Explain(ctx, c, req.Msg)
	if err != nil {
		return nil, ToConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Ask(ctx context.Context, req *connect.Request[oapv1.AskRequest]) (*connect.Response[oapv1.AskResponse], error) {
	c, err := s.who(ctx, req.Header())
	if err != nil {
		return nil, ToConnect(err)
	}
	out, err := s.Service.Ask(ctx, c, req.Msg)
	if err != nil {
		return nil, ToConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Login(ctx context.Context, req *connect.Request[oapv1.LoginRequest], stream *connect.ServerStream[oapv1.LoginResponse]) error {
	return ToConnect(s.Service.Login(ctx, req.Msg, stream.Send))
}

// BriefingPath is where the briefing is served, for any harness to load.
const BriefingPath = "/.well-known/oap/briefing.md"

// BriefingHandler serves the caller's briefing as Markdown; ?app=NAME
// limits it to one application.
func BriefingHandler(svc *service.Service, auth func(context.Context, http.Header) (service.Caller, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		c, err := auth(r.Context(), r.Header)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		md, err := svc.Briefing(r.Context(), c, r.URL.Query().Get("app"))
		if err != nil {
			status := http.StatusInternalServerError
			var se *service.Error
			if errors.As(err, &se) {
				status = map[service.Code]int{service.NotFound: http.StatusNotFound, service.PermissionDenied: http.StatusForbidden,
					service.Unauthenticated: http.StatusUnauthorized}[se.Code]
				if status == 0 {
					status = http.StatusBadRequest
				}
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, md)
	})
}

// Paths of the guides for agents (R3.16): each skill as Markdown, and the
// JSON Schema of oap.yaml.
const (
	SkillsPath = "/.well-known/oap/skills/"
	SchemaPath = "/.well-known/oap/schema/application.json"
)

// GuideHandler serves skills at SkillsPath+NAME.md and the schema at
// SchemaPath, to any signed-in caller.
func GuideHandler(svc *service.Service, auth func(context.Context, http.Header) (service.Caller, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		c, err := auth(r.Context(), r.Header)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == SchemaPath {
			w.Header().Set("Content-Type", "application/schema+json")
			w.Write(svc.ApplicationSchema(r.Context(), c))
			return
		}
		name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, SkillsPath), ".md")
		if !ok {
			http.NotFound(w, r)
			return
		}
		sk, err := svc.Skill(r.Context(), c, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		io.WriteString(w, sk.Markdown)
	})
}
