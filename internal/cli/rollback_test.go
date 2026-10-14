package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRollbackCLIReviewAndAdmissionRemainSeparate(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if r.Method != "GET" || r.URL.Path != "/api/v1/applications/app-1/rollback-plan" || r.URL.Query().Get("releaseId") != "release-1" || r.URL.Query().Get("expectedVersion") != "3" || r.ContentLength > 0 {
				t.Error("invalid review request")
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"planHash":"reviewed"}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/v1/applications/app-1/rollbacks" || r.Header.Get("Idempotency-Key") != "rollback-1" {
			t.Error("invalid execution request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write([]byte(`{"id":"operation-1","state":"queued"}`))
	}))
	defer api.Close()
	args := []string{"--url", api.URL, "--app", "app-1", "--file", "-"}
	if _, code := runTest(t, append(args, "rollback-plan"), strings.Repeat("t", 32), `{"releaseId":"release-1","expectedVersion":3}`); code != 0 || calls != 1 {
		t.Fatal("review failed")
	}
	input := `{"releaseId":"release-1","expectedVersion":3,"planHash":"` + strings.Repeat("a", 64) + `","acknowledgeDataCompatibility":true}`
	if _, code := runTest(t, append(args, "rollback"), strings.Repeat("t", 32), input); code != 2 || calls != 1 {
		t.Fatal("missing key reached mutation")
	}
	result, code := runTest(t, append(args, "--key", "rollback-1", "rollback"), strings.Repeat("t", 32), input)
	if code != 0 || result.OperationID != "operation-1" || result.IdempotencyKey != "rollback-1" || calls != 2 {
		t.Fatal("admission did not expose durable handle", result, code)
	}
}

func TestVerificationSeparatesJobCompletionFromWorkloadHealth(t *testing.T) {
	for _, tc := range []struct {
		kind, status string
		want         int
	}{{"job", "completed:0", 0}, {"job", "running", 2}, {"web", "completed:0", 2}} {
		t.Run(tc.kind+tc.status, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[{"kind":"` + tc.kind + `","status":"` + tc.status + `","retired":false}]`))
			}))
			defer api.Close()
			_, code := runTest(t, []string{"--url", api.URL, "--app", "app-1", "verify"}, strings.Repeat("t", 32), "")
			if code != tc.want {
				t.Fatal(code, tc.want)
			}
		})
	}
}
