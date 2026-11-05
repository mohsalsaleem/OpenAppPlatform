package ops

import (
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

func TestSecurityReachesInstancesAndJobs(t *testing.T) {
	sec := &spec.Security{Capabilities: []string{"CHOWN"}, WritableRoot: true, ExceptionReason: "entrypoint chowns its data"}
	app := &spec.Application{Name: "shop", Components: map[string]spec.Component{
		"web":     {Kind: spec.KindWeb, Image: "web:1", Port: 80, Instances: 1, Security: sec, Resources: &spec.Resources{MemoryMiB: 128, CPUMillis: 500, Pids: 64}},
		"migrate": {Kind: spec.KindJob, Image: "m:1", Instances: 1, Resources: &spec.Resources{MemoryMiB: 96, CPUMillis: 250}},
		"plain":   {Kind: spec.KindWorker, Image: "w:1", Instances: 1},
	}}
	got := InstanceSpecFor(app, "web", 1).Security
	if strings.Join(got.Capabilities, ",") != "CHOWN" || !got.WritableRoot || got.AllowPrivilegeEscalation || got.Pids != 64 {
		t.Fatalf("web: %+v", got)
	}
	if s := InstanceSpecFor(app, "plain", 1).Security; s.WritableRoot || len(s.Capabilities) > 0 || s.Pids != 0 || s.AllowPrivilegeEscalation {
		t.Fatalf("defaults must be the zero value: %+v", s)
	}
	if j := JobSpecFor(app, "migrate"); j.MemoryMiB != 96 || j.CPUMillis != 250 {
		t.Fatalf("job resources: %+v", j)
	}
	// The spec's slice is copied, so the runtime can't change the spec.
	got.Capabilities[0] = "KILL"
	if sec.Capabilities[0] != "CHOWN" {
		t.Fatal("capabilities not copied")
	}
}
