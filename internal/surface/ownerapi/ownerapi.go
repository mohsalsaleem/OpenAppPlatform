// Package ownerapi serves OwnerService over Connect: the owner's control
// over agents. Every method refuses callers other than the owner.
package ownerapi

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
)

// Server adapts the service's owner operations to the generated handler.
type Server struct {
	Service *service.Service
	// Auth resolves a request's caller.
	Auth func(context.Context, http.Header) (service.Caller, error)
}

var _ oapv1connect.OwnerServiceHandler = (*Server)(nil)

// Handler returns the HTTP path and handler for the owner API.
func (s *Server) Handler() (string, http.Handler) {
	return oapv1connect.NewOwnerServiceHandler(s, connect.WithCodec(agentapi.JSONCodec{}))
}

// call resolves the caller, runs fn and translates errors.
func call[Req, Res any](s *Server, ctx context.Context, req *connect.Request[Req], fn func(context.Context, service.Caller, *Req) (*Res, error)) (*connect.Response[Res], error) {
	c, err := s.Auth(ctx, req.Header())
	if err != nil {
		return nil, agentapi.ToConnect(err)
	}
	out, err := fn(ctx, c, req.Msg)
	if err != nil {
		return nil, agentapi.ToConnect(err)
	}
	if out == nil {
		return nil, agentapi.ToConnect(errors.New("ownerapi: empty response"))
	}
	return connect.NewResponse(out), nil
}

func (s *Server) ListLogins(ctx context.Context, req *connect.Request[oapv1.ListLoginsRequest]) (*connect.Response[oapv1.ListLoginsResponse], error) {
	return call(s, ctx, req, func(ctx context.Context, c service.Caller, _ *oapv1.ListLoginsRequest) (*oapv1.ListLoginsResponse, error) {
		return s.Service.ListLogins(ctx, c)
	})
}

func (s *Server) ApproveLogin(ctx context.Context, req *connect.Request[oapv1.ApproveLoginRequest]) (*connect.Response[oapv1.ApproveLoginResponse], error) {
	return call(s, ctx, req, s.Service.ApproveLogin)
}

func (s *Server) DenyLogin(ctx context.Context, req *connect.Request[oapv1.DenyLoginRequest]) (*connect.Response[oapv1.DenyLoginResponse], error) {
	return call(s, ctx, req, s.Service.DenyLogin)
}

func (s *Server) ListAgents(ctx context.Context, req *connect.Request[oapv1.ListAgentsRequest]) (*connect.Response[oapv1.ListAgentsResponse], error) {
	return call(s, ctx, req, func(ctx context.Context, c service.Caller, _ *oapv1.ListAgentsRequest) (*oapv1.ListAgentsResponse, error) {
		return s.Service.ListAgents(ctx, c)
	})
}

func (s *Server) Revoke(ctx context.Context, req *connect.Request[oapv1.RevokeRequest]) (*connect.Response[oapv1.RevokeResponse], error) {
	return call(s, ctx, req, s.Service.Revoke)
}

func (s *Server) Pause(ctx context.Context, req *connect.Request[oapv1.PauseRequest]) (*connect.Response[oapv1.PauseResponse], error) {
	return call(s, ctx, req, s.Service.Pause)
}

func (s *Server) Resume(ctx context.Context, req *connect.Request[oapv1.ResumeRequest]) (*connect.Response[oapv1.ResumeResponse], error) {
	return call(s, ctx, req, s.Service.Resume)
}

func (s *Server) SetApprovalMode(ctx context.Context, req *connect.Request[oapv1.SetApprovalModeRequest]) (*connect.Response[oapv1.SetApprovalModeResponse], error) {
	return call(s, ctx, req, s.Service.SetApprovalMode)
}

func (s *Server) ListAsks(ctx context.Context, req *connect.Request[oapv1.ListAsksRequest]) (*connect.Response[oapv1.ListAsksResponse], error) {
	return call(s, ctx, req, func(ctx context.Context, c service.Caller, _ *oapv1.ListAsksRequest) (*oapv1.ListAsksResponse, error) {
		return s.Service.ListAsks(ctx, c)
	})
}

func (s *Server) AnswerAsk(ctx context.Context, req *connect.Request[oapv1.AnswerAskRequest]) (*connect.Response[oapv1.AnswerAskResponse], error) {
	return call(s, ctx, req, s.Service.AnswerAsk)
}
