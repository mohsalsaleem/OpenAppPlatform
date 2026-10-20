package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

var n = ops.Notification{Kind: "ask", Subject: "claude asks: deploy now?", Body: "oapctl owner answer ask_1 approve"}

func TestWebhookSignsAndRetries(t *testing.T) {
	var calls atomic.Int32
	var got payload
	var sig string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		m := hmac.New(sha256.New, []byte("s3cret"))
		m.Write(b)
		if r.Header.Get("X-OAP-Signature") != "sha256="+hex.EncodeToString(m.Sum(nil)) {
			sig = "bad"
		}
	}))
	defer ts.Close()
	w := Webhook{URL: ts.URL, Secret: "s3cret", Backoff: time.Millisecond}
	if err := w.Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || got.Kind != "ask" || got.Subject != n.Subject || got.SentAt == "" || sig != "" {
		t.Fatalf("calls %d, payload %+v, signature %q", calls.Load(), got, sig)
	}
}

func TestWebhookGivesUp(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()
	if err := (Webhook{URL: ts.URL, Backoff: time.Millisecond}).Notify(context.Background(), n); err == nil || calls.Load() != 1 {
		t.Fatalf("a 400 must not be retried: %v after %d calls", err, calls.Load())
	}
	down := Webhook{URL: "http://127.0.0.1:1", Attempts: 2, Backoff: time.Millisecond}
	if err := down.Notify(context.Background(), n); err == nil {
		t.Fatal("an unreachable webhook succeeded")
	}
}
