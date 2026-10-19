// Package agentapi serves the agent API over Connect, which speaks gRPC,
// gRPC-Web and Connect's JSON protocol on one handler. It translates
// between the wire and the service and holds no logic of its own; every
// error carries its why code and remedy as a typed detail.
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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
	// Caller is who every request on this handler comes from. Until device
	// login (R3.6) the handler serves only oapd's local socket, reached by
	// the owner over SSH.
	Caller service.Caller
}

var _ oapv1connect.AgentServiceHandler = (*Server)(nil)

// Handler returns the HTTP path and handler for the agent API. JSON follows
// OAP's wire conventions (field names as written in the protos), not
// protojson's default lowerCamelCase.
func (s *Server) Handler() (string, http.Handler) {
	return oapv1connect.NewAgentServiceHandler(s, connect.WithCodec(jsonCodec{}))
}

// jsonCodec is Connect's "json" codec with OAP's JSON conventions.
type jsonCodec struct{}

func (jsonCodec) Name() string { return "json" }

func (jsonCodec) Marshal(v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("agentapi: %T isn't a protobuf message", v)
	}
	return wire.Marshal(m)
}

func (jsonCodec) Unmarshal(b []byte, v any) error {
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
}

// toConnect turns a service error into a Connect error with the ErrorDetail.
func toConnect(err error) error {
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
	out, err := s.Service.Observe(ctx, s.Caller, req.Msg)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Watch(ctx context.Context, req *connect.Request[oapv1.WatchRequest], stream *connect.ServerStream[oapv1.WatchResponse]) error {
	return toConnect(&service.Error{Code: service.Unimplemented, Detail: &oapv1.ErrorDetail{Why: service.WhyNotAvailable, Message: "watch isn't available yet (task R3.3); use await for an operation"}})
}

func (s *Server) Propose(ctx context.Context, req *connect.Request[oapv1.ProposeRequest]) (*connect.Response[oapv1.ProposeResponse], error) {
	out, err := s.Service.Propose(ctx, s.Caller, req.Msg)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Apply(ctx context.Context, req *connect.Request[oapv1.ApplyRequest]) (*connect.Response[oapv1.ApplyResponse], error) {
	out, err := s.Service.Apply(ctx, s.Caller, req.Msg)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Await(ctx context.Context, req *connect.Request[oapv1.AwaitRequest], stream *connect.ServerStream[oapv1.AwaitResponse]) error {
	return toConnect(s.Service.Await(ctx, s.Caller, req.Msg, func(e *oapv1.Event) error {
		return stream.Send(&oapv1.AwaitResponse{Event: e})
	}))
}

func (s *Server) Explain(ctx context.Context, req *connect.Request[oapv1.ExplainRequest]) (*connect.Response[oapv1.ExplainResponse], error) {
	out, err := s.Service.Explain(ctx, s.Caller, req.Msg)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Ask(ctx context.Context, req *connect.Request[oapv1.AskRequest]) (*connect.Response[oapv1.AskResponse], error) {
	out, err := s.Service.Ask(ctx, s.Caller, req.Msg)
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(out), nil
}

func (s *Server) Login(ctx context.Context, req *connect.Request[oapv1.LoginRequest], stream *connect.ServerStream[oapv1.LoginResponse]) error {
	return toConnect(s.Service.Login(ctx, req.Msg, stream.Send))
}
