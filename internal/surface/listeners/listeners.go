// Package listeners assembles oapd's two HTTP surfaces, so oapd and the
// security tests (R3.9) serve exactly the same routes.
//
// On the local socket, reached over SSH, a request without a token is the
// owner's; with one it is that agent's (or an owner session's). The public
// listener requires a token for everything but login; its owner API takes
// only owner sessions, signed in with an enrolled owner key.
package listeners

import (
	"fmt"
	"net/http"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/mcp"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/ownerapi"
)

func healthz(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") }

// Socket is the local socket's mux: agent API, owner API, MCP, briefing.
func Socket(svc *service.Service) *http.ServeMux {
	auth := agentapi.Bearer(svc, &service.Owner)
	mux := http.NewServeMux()
	mux.Handle((&agentapi.Server{Service: svc, Caller: &service.Owner}).Handler())
	mux.Handle((&ownerapi.Server{Service: svc, Auth: auth}).Handler())
	mux.Handle("/mcp", &mcp.Server{Service: svc, Caller: service.Owner, Auth: auth})
	mux.Handle(agentapi.BriefingPath, agentapi.BriefingHandler(svc, auth))
	mux.HandleFunc("/healthz", healthz)
	return mux
}

// Public is the public listener's mux: agent API, owner API, MCP and
// briefing, each requiring a token except login.
func Public(svc *service.Service) *http.ServeMux {
	auth := agentapi.Bearer(svc, nil)
	mux := http.NewServeMux()
	mux.Handle((&agentapi.Server{Service: svc}).Handler())
	mux.Handle((&ownerapi.Server{Service: svc, Auth: auth}).Handler())
	mux.Handle("/mcp", &mcp.Server{Service: svc, Auth: auth})
	mux.Handle(agentapi.BriefingPath, agentapi.BriefingHandler(svc, auth))
	mux.HandleFunc("/healthz", healthz)
	return mux
}
