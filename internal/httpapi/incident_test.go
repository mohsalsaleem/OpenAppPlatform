package httpapi

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func TestIncidentErrorsDistinguishMissingAppFromMetadataOutage(t *testing.T) {
	response := httptest.NewRecorder()
	incidentError(response, errors.New("private database connection detail"))
	var result struct {
		Code        string   `json:"code"`
		Retryable   bool     `json:"retryable"`
		NextActions []string `json:"nextActions"`
	}
	if response.Code != 503 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Code != "incident_unavailable" || !result.Retryable || len(result.NextActions) == 0 || strings.Contains(response.Body.String(), "private database") {
		t.Fatal("metadata outage was misclassified or exposed")
	}
	response = httptest.NewRecorder()
	incidentError(response, domain.ErrNotFound)
	if response.Code != 404 {
		t.Fatal("missing app became service outage")
	}
}
