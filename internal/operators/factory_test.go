package operators

import (
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestRegistryRejectsSecretBearingUnsupportedSettings(t *testing.T) {
	target := domain.Target{ID: "coolify", Name: "Coolify", Operator: "coolify", Environment: "staging", URL: "https://example.com", ProjectID: "p", ServerID: "s", Settings: json.RawMessage(`{"token":"secret"}`)}
	if _, e := New(target); e == nil {
		t.Fatal("unknown credentials would be persisted in public settings")
	}
}
