package httpapi

import "testing"

func TestMachineErrorsPreserveCodeAndSupplyHints(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429, 500, 503} {
		got := machineError(status, map[string]string{"code": "specific_cause", "message": "existing message"}).(map[string]any)
		if got["code"] != "specific_cause" || len(got["nextActions"].([]string)) == 0 {
			t.Fatal(status, got)
		}
		if got["retryable"] != (status == 429 || status >= 500) {
			t.Fatal("unsafe retry hint")
		}
	}
	if machineError(403, map[string]string{"message": "denied"}).(map[string]any)["code"] != "forbidden" {
		t.Fatal("missing code")
	}
}
