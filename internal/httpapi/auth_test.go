package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthenticationRequiresBearerScheme(t *testing.T) {
	token := strings.Repeat("s", 32)
	handler := (&Server{Preview: true, Token: token}).Handler()
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

func TestLogRedactionPreservesSessionOutputAndRemovesPreviewToken(t *testing.T) {
	for _, tc := range []struct{ name, logs, token, want string }{
		{"owner session", "hello 🌍\nfixture ready\n", "", "hello 🌍\nfixture ready\n"},
		{"preview token", "secret=preview-token\npreview-token", "preview-token", "secret=[REDACTED]\n[REDACTED]"},
		{"unrelated logs", "fixture ready", "preview-token", "fixture ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactToken(tc.logs, tc.token); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
