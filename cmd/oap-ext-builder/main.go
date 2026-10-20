// Command oap-ext-builder is OAP's on-host builder extension (R4.2): it
// clones a repository at a commit and builds its image with the host's
// Docker Engine, every build step held to the memory the request names.
// oapd's build queue runs one build at a time by default.
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		Manifest: &extensionv1.Manifest{Name: "builder", Version: "1",
			Description: "Builds images on this server with its Docker Engine.",
			Points:      []extensionv1.Point{extensionv1.Point_POINT_BUILDER},
			Capabilities: []*extensionv1.Capability{
				{Kind: &extensionv1.Capability_Setting{Setting: "docker_socket"}, Why: "the Docker Engine to build with (default /var/run/docker.sock)"},
				{Kind: &extensionv1.Capability_Setting{Setting: "install_id"}, Why: "to label the images it builds as this installation's"},
				{Kind: &extensionv1.Capability_Secret{Secret: "git-token"}, Why: "to clone private repositories over https (optional)"},
				{Kind: &extensionv1.Capability_Network{Network: "git hosts:443"}, Why: "to clone repositories"},
			}},
		Configure: func(ctx context.Context, s, sec map[string]string) error {
			mu.Lock()
			defer mu.Unlock()
			settings, secrets = s, sec
			return nil
		},
		Builder: build,
	})
}

func build(ctx context.Context, req *extensionv1.BuildRequest) (*extensionv1.BuildResponse, error) {
	mu.Lock()
	socket, install, token := settings["docker_socket"], settings["install_id"], secrets["git-token"]
	mu.Unlock()
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	tmp, err := os.MkdirTemp("", "oap-build-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, "src")
	commit, err := dockerapi.Checkout(ctx, req.Repository, req.Commit, src, token)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(src, filepath.FromSlash(req.Context))
	if rel, err := filepath.Rel(src, dir); err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) > 1 && rel[:2] == ".." {
		return nil, errors.New("the context is outside the repository")
	}
	image := req.Image + ":" + commit[:12]
	labels := map[string]string{"io.openappplatform.application": req.Application, "io.openappplatform.component": req.Component,
		"io.openappplatform.commit": commit, "io.openappplatform.role": "build"}
	if install != "" {
		labels["io.openappplatform.install"] = install
	}
	tail, err := dockerapi.New(socket).Build(ctx, dir, dockerapi.BuildOptions{Tag: image, Dockerfile: req.Dockerfile,
		MemoryMiB: int(req.MemoryMib), Labels: labels})
	if err != nil {
		return nil, errors.New(err.Error() + "\n" + tail)
	}
	return &extensionv1.BuildResponse{Image: image, Commit: commit, LogTail: tail}, nil
}
