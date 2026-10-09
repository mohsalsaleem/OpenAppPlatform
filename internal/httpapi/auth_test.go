package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticationRequiresBearerScheme(t *testing.T) {
	token := strings.Repeat("s", 32)
	handler := (&Server{Token: token}).Handler()
	for _, header := range []string{"", token, "Basic " + token, "Bearer wrong"} {
		req := httptest.NewRequest("GET", "/api/v1/meta", nil)
		req.Header.Set("Authorization", header)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatal("accepted invalid scheme")
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/meta", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatal("rejected valid authentication")
	}
}
