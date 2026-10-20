// Command oap-ext-registry is OAP's registry-only builder extension (R4.2):
// it builds nothing. It resolves the commit, fills in the owner's image
// template (for example ghcr.io/acme/notes:{commit}), and pulls the image
// to prove the registry has it.
package main

import (
	"context"
	"errors"
	"strings"
	"sync"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension/dockerapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension/sdk"
)

var (
	mu       sync.Mutex
	settings = map[string]string{}
	secrets  = map[string]string{}
)

func main() {
	sdk.Serve(sdk.Extension{
		Manifest: &extensionv1.Manifest{Name: "registry", Version: "1",
			Description: "Uses images a registry already has for each commit; builds nothing.",
			Points:      []extensionv1.Point{extensionv1.Point_POINT_BUILDER},
			Capabilities: []*extensionv1.Capability{
				{Kind: &extensionv1.Capability_Setting{Setting: "image_template"}, Why: "the image for a commit, with {commit} or {short}"},
				{Kind: &extensionv1.Capability_Setting{Setting: "docker_socket"}, Why: "the Docker Engine that pulls (default /var/run/docker.sock)"},
				{Kind: &extensionv1.Capability_Secret{Secret: "git-token"}, Why: "to resolve branches of private repositories (optional)"},
				{Kind: &extensionv1.Capability_Network{Network: "registries and git hosts:443"}, Why: "to resolve commits and pull images"},
			}},
		Configure: func(ctx context.Context, s, sec map[string]string) error {
			mu.Lock()
			defer mu.Unlock()
			settings, secrets = s, sec
			return nil
		},
		Builder: resolve,
	})
}

func resolve(ctx context.Context, req *extensionv1.BuildRequest) (*extensionv1.BuildResponse, error) {
	mu.Lock()
	tmpl, socket, token := settings["image_template"], settings["docker_socket"], secrets["git-token"]
	mu.Unlock()
	if !strings.Contains(tmpl, "{commit}") && !strings.Contains(tmpl, "{short}") {
		return nil, errors.New("the image_template setting needs {commit} or {short}")
	}
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	commit, err := dockerapi.ResolveRemote(ctx, req.Repository, req.Commit, token)
	if err != nil {
		return nil, err
	}
	image := strings.NewReplacer("{commit}", commit, "{short}", commit[:12]).Replace(tmpl)
	tail, err := dockerapi.New(socket).Pull(ctx, image)
	if err != nil {
		return nil, err
	}
	return &extensionv1.BuildResponse{Image: image, Commit: commit, LogTail: tail}, nil
}
