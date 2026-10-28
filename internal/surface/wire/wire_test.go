package wire

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name string, m proto.Message) {
	t.Helper()
	got, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, got, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", name+".json")
	if *update {
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Fatalf("%s differs from golden file:\n%s", name, pretty.String())
	}
	// Round trip: decoding the golden JSON gives back the same message.
	back := m.ProtoReflect().New().Interface()
	if err := Unmarshal(want, back); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(m, back) {
		t.Fatalf("%s does not round-trip", name)
	}
}

var at = timestamppb.New(time.Date(2026, 10, 19, 14, 5, 0, 0, time.UTC))

func TestGoldenProposal(t *testing.T) {
	golden(t, "proposal", &oapv1.Proposal{
		Id:           "prop_7Hq2",
		BaseRevision: 42,
		Intent: &oapv1.Intent{
			Application: "shop",
			Because:     "p95 latency above target since 14:05",
			Change:      &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "api", Instances: 3}},
		},
		Verdict: oapv1.Verdict_VERDICT_ALLOWED,
		Risk:    oapv1.Risk_RISK_ROUTINE,
		Steps: []*oapv1.Step{
			{Kind: oapv1.StepKind_STEP_KIND_START_INSTANCE, Component: "api", Ordinal: 2},
			{Kind: oapv1.StepKind_STEP_KIND_AWAIT_READY, Component: "api", Ordinal: 2, After: []int32{0}},
			{Kind: oapv1.StepKind_STEP_KIND_ROUTE_ADD, Component: "api", Ordinal: 2, After: []int32{1}},
		},
		BlastRadius: &oapv1.BlastRadius{Components: []string{"api"}, Downtime: "none-expected"},
		Undo: &oapv1.Intent{
			Application: "shop",
			Change:      &oapv1.Intent_Scale{Scale: &oapv1.Scale{Component: "api", Instances: 1}},
		},
		Next: []string{"apply"},
	})
}

func TestGoldenOutcomeEvent(t *testing.T) {
	golden(t, "outcome_event", &oapv1.AwaitResponse{Event: &oapv1.Event{
		Revision: 57,
		At:       at,
		Kind: &oapv1.Event_Outcome{Outcome: &oapv1.Outcome{
			OperationId: "op_1",
			Status:      oapv1.OutcomeStatus_OUTCOME_STATUS_NOT_ACHIEVED,
			Reasons:     []*oapv1.Reason{{Why: "traffic.requests_failed", Message: "2 requests failed during scale-down"}},
			Evidence: &oapv1.Evidence{Traffic: &oapv1.TrafficSummary{
				Requests: 277, Failures: 2,
				Failed: []*oapv1.RequestFailure{{At: at, Target: "api-3", Reason: "connection reset"}},
			}},
		}},
	}})
}

func TestGoldenConfigureWithPresence(t *testing.T) {
	zero := int32(0)
	patch, err := structpb.NewStruct(map[string]any{"replicas": 2})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "configure", &oapv1.Intent{
		Application: "shop",
		Change: &oapv1.Intent_Configure{Configure: &oapv1.Configure{
			Component:      "api",
			SetEnv:         map[string]string{"LOG_LEVEL": "info", "FEATURE_X": "on"},
			MemoryLimitMib: proto.Int32(128),
			CpuMillis:      &zero, // explicitly set to zero: present in JSON
		}},
	})
	golden(t, "spec_patch", &oapv1.Intent{
		Application: "shop",
		Change:      &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: patch}},
	})
}

func TestConventions(t *testing.T) {
	b, err := Marshal(&oapv1.ProposeRequest{
		ExpectedRevision: 9,
		Intent:           &oapv1.Intent{Change: &oapv1.Intent_Rollback{Rollback: &oapv1.Rollback{ToRelease: "r7"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"expected_revision":"9"`, `"to_release":"r7"`} {
		if !strings.Contains(s, want) {
			t.Errorf("want %s in %s", want, s)
		}
	}
	// Unset fields are omitted, including zero-valued enums and empty lists.
	if strings.Contains(s, "because") || strings.Contains(s, "application") {
		t.Errorf("unset fields should be omitted: %s", s)
	}
	b, err = Marshal(&oapv1.Affordance{Action: "scale", Verdict: oapv1.Verdict_VERDICT_NEEDS_APPROVAL})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"action":"scale","verdict":"VERDICT_NEEDS_APPROVAL"}` {
		t.Errorf("enums should be strings: %s", b)
	}
}

func TestUnmarshalIsLenientInNamesStrictInFields(t *testing.T) {
	for _, in := range []string{
		`{"expected_revision": 9}`,
		`{"expected_revision": "9"}`,
		`{"expectedRevision": 9}`,
	} {
		var req oapv1.ProposeRequest
		if err := Unmarshal([]byte(in), &req); err != nil || req.ExpectedRevision != 9 {
			t.Errorf("%s: got %v, %v", in, req.ExpectedRevision, err)
		}
	}
	var req oapv1.ProposeRequest
	if err := Unmarshal([]byte(`{"expected_revison": 9}`), &req); err == nil {
		t.Error("unknown field should be rejected, so agents learn about typos")
	}
	var aff oapv1.Affordance
	if err := Unmarshal([]byte(`{"verdict":"VERDICT_MAYBE"}`), &aff); err == nil {
		t.Error("unknown enum value should be rejected")
	}
}

// documented64 lists the only 64-bit integer fields allowed in the API. Agents
// see them as decimal strings in JSON (proto/README.md).
var documented64 = map[string]bool{
	"oap.v1.World.revision":                   true,
	"oap.v1.Event.revision":                   true,
	"oap.v1.JournalEntry.base_revision":       true,
	"oap.v1.Proposal.base_revision":           true,
	"oap.v1.WatchRequest.since_revision":      true,
	"oap.v1.AwaitRequest.since_revision":      true,
	"oap.v1.ProposeRequest.expected_revision": true,
	"oap.v1.ExplainResponse.revision":         true,
}

func TestOnlyDocumented64BitFields(t *testing.T) {
	var found []string
	files := 0
	protoregistry.GlobalFiles.RangeFilesByPackage("oap.v1", func(fd protoreflect.FileDescriptor) bool {
		files++
		msgs := fd.Messages()
		for i := 0; i < msgs.Len(); i++ {
			found = append(found, sixtyFour(msgs.Get(i))...)
		}
		return true
	})
	if files < 7 {
		t.Fatalf("found %d oap.v1 files; expected all seven to be registered", files)
	}
	sort.Strings(found)
	seen := map[string]bool{}
	for _, f := range found {
		seen[f] = true
		if !documented64[f] {
			t.Errorf("%s is a 64-bit integer; use int32, or document it in proto/README.md and here", f)
		}
	}
	for f := range documented64 {
		if !seen[f] {
			t.Errorf("%s is documented as 64-bit but no longer exists; update the list", f)
		}
	}
}

func sixtyFour(md protoreflect.MessageDescriptor) []string {
	var out []string
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		switch fields.Get(i).Kind() {
		case protoreflect.Int64Kind, protoreflect.Uint64Kind, protoreflect.Sint64Kind,
			protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
			out = append(out, string(fields.Get(i).FullName()))
		}
	}
	nested := md.Messages()
	for i := 0; i < nested.Len(); i++ {
		out = append(out, sixtyFour(nested.Get(i))...)
	}
	return out
}
