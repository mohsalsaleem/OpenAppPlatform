// Package extension is OAP's extension host (R4.1): it runs extensions out
// of process as go-plugin gRPC plugins, gives each only the settings and
// secrets the owner granted, bounds every call with a timeout, kills an
// extension that exceeds its memory cap, and restarts a crashed or hung one
// on its next use. A failing extension is an error to the core, never a
// crash of it. Extensions supply facts and side effects; the contract
// (proto/oap/extension/v1) has no field that can carry a policy verdict.
package extension

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Handshake is what the host and an extension agree on before talking.
// The cookie only stops an extension binary being run by hand by mistake.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "OAP_EXTENSION",
	MagicCookieValue: "b7f3c1e2-oap-extension-v1",
}

// ContractVersion is the extension contract the host speaks.
const ContractVersion = 1

// pluginName is the one plugin every extension binary serves.
const pluginName = "oap"

// Service is a gRPC service an extension serves, described by hand the way
// protoc-gen-go-grpc would generate it.
type Service struct {
	Desc *grpc.ServiceDesc
	Impl any
}

// Unary describes one unary method whose handler calls fn.
func Unary[Req, Res proto.Message](name string, newReq func() Req, fn func(context.Context, Req) (Res, error)) grpc.MethodDesc {
	return grpc.MethodDesc{MethodName: name, Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		req := newReq()
		if err := dec(req); err != nil {
			return nil, err
		}
		return fn(ctx, req)
	}}
}

// grpcPlugin carries services to the extension's gRPC server, or hands the
// host the connection to call them.
type grpcPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	services []Service
}

func (p *grpcPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	for _, svc := range p.services {
		s.RegisterService(svc.Desc, svc.Impl)
	}
	return nil
}

func (p *grpcPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return c, nil
}

// Serve runs an extension's services; it is the whole of an extension's
// main once its services are described (see package sdk).
func Serve(services ...Service) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         plugin.PluginSet{pluginName: &grpcPlugin{services: services}},
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}
