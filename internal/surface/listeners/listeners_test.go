package listeners

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
)

// TestRequestsAreBounded (R5.8): a request body larger than MaxRequestBytes
// is refused on both listeners, whoever sends it, before it is handled.
func TestRequestsAreBounded(t *testing.T) {
	clock := sim.NewClock(time.Now())
	rt, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), &sim.Journal{}, sim.NewDesired()
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: sim.NewEdge(), Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	huge := `{"agent": "` + strings.Repeat("a", 6<<20) + `"}`
	for name, mux := range map[string]*http.ServeMux{"public": Public(svc), "socket": Socket(svc)} {
		srv := httptest.NewServer(mux)
		for _, path := range []string{"/oap.v1.AgentService/Login", "/oap.v1.AgentService/Observe", "/oap.v1.OwnerService/ListAgents"} {
			ct := "application/json"
			if strings.HasSuffix(path, "Login") {
				ct = "application/connect+json" // a server stream
			}
			res, err := http.Post(srv.URL+path, ct, strings.NewReader(huge))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if !strings.Contains(string(b), "resource_exhausted") {
				t.Errorf("%s %s: %d %.200s", name, path, res.StatusCode, b)
			}
		}
		srv.Close()
	}
}
