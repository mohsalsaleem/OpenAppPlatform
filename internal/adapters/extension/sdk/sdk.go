// Package sdk is what an OAP extension is written with: describe the
// extension, implement the points it serves, and call Serve from main.
package sdk

import (
	"context"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension"
	"google.golang.org/grpc"
)

// Extension is an extension's implementation. Configure receives only the
// settings and secrets the owner granted. Notifier is set for extensions
// that serve the notifier point.
type Extension struct {
	Manifest  *extensionv1.Manifest
	Configure func(ctx context.Context, settings, secrets map[string]string) error
	Notifier  func(ctx context.Context, req *extensionv1.NotifyRequest) (*extensionv1.NotifyResponse, error)
	// Builder is set for extensions that serve the builder point.
	Builder func(ctx context.Context, req *extensionv1.BuildRequest) (*extensionv1.BuildResponse, error)
}

// Serve runs the extension until the host stops it.
func Serve(e Extension) {
	services := []extension.Service{{Desc: extensionDesc(e), Impl: struct{}{}}}
	if e.Notifier != nil {
		services = append(services, extension.Service{Desc: &grpc.ServiceDesc{
			ServiceName: "oap.extension.v1.NotifierService",
			HandlerType: (*any)(nil),
			Methods: []grpc.MethodDesc{extension.Unary("Notify",
				func() *extensionv1.NotifyRequest { return &extensionv1.NotifyRequest{} }, e.Notifier)},
			Metadata: "oap/extension/v1/notifier.proto",
		}, Impl: struct{}{}})
	}
	if e.Builder != nil {
		services = append(services, extension.Service{Desc: &grpc.ServiceDesc{
			ServiceName: "oap.extension.v1.BuilderService",
			HandlerType: (*any)(nil),
			Methods: []grpc.MethodDesc{extension.Unary("Build",
				func() *extensionv1.BuildRequest { return &extensionv1.BuildRequest{} }, e.Builder)},
			Metadata: "oap/extension/v1/builder.proto",
		}, Impl: struct{}{}})
	}
	extension.Serve(services...)
}

func extensionDesc(e Extension) *grpc.ServiceDesc {
	return &grpc.ServiceDesc{
		ServiceName: "oap.extension.v1.ExtensionService",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{
			extension.Unary("Describe", func() *extensionv1.DescribeRequest { return &extensionv1.DescribeRequest{} },
				func(context.Context, *extensionv1.DescribeRequest) (*extensionv1.DescribeResponse, error) {
					return &extensionv1.DescribeResponse{Manifest: e.Manifest}, nil
				}),
			extension.Unary("Configure", func() *extensionv1.ConfigureRequest { return &extensionv1.ConfigureRequest{} },
				func(ctx context.Context, req *extensionv1.ConfigureRequest) (*extensionv1.ConfigureResponse, error) {
					if e.Configure != nil {
						if err := e.Configure(ctx, req.Settings, req.Secrets); err != nil {
							return nil, err
						}
					}
					return &extensionv1.ConfigureResponse{}, nil
				}),
		},
		Metadata: "oap/extension/v1/extension.proto",
	}
}
