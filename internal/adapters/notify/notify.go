// Package notify delivers notifications to the owner (ports.Notifier):
// to oapd's log, or to a webhook as signed JSON.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

// Log writes notifications to a logger.
type Log struct{ Logger *slog.Logger }

func (l Log) Notify(ctx context.Context, n ops.Notification) error {
	l.Logger.Info("notification", "kind", n.Kind, "subject", n.Subject, "body", n.Body)
	return nil
}

// Webhook POSTs each notification as JSON:
// {"kind", "subject", "body", "sent_at"}. With a secret, the
// X-OAP-Signature header is "sha256=" and the hex HMAC-SHA256 of the
// body, so the receiver can check it came from OAP. Failures are retried
// with backoff; a 4xx other than 429 isn't.
type Webhook struct {
	URL    string
	Secret string
	Client *http.Client
	// Attempts is how many times to try; 3 when zero.
	Attempts int
	// Backoff is the wait before the second attempt, doubling after; 1 s
	// when zero.
	Backoff time.Duration
	Now     func() time.Time
}

type payload struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	SentAt  string `json:"sent_at"`
}

func (w Webhook) Notify(ctx context.Context, n ops.Notification) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	body, err := json.Marshal(payload{Kind: n.Kind, Subject: n.Subject, Body: n.Body, SentAt: now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	attempts, backoff := w.Attempts, w.Backoff
	if attempts <= 0 {
		attempts = 3
	}
	if backoff <= 0 {
		backoff = time.Second
	}
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "oapd")
		if w.Secret != "" {
			m := hmac.New(sha256.New, []byte(w.Secret))
			m.Write(body)
			req.Header.Set("X-OAP-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
		}
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode/100 == 2:
			return nil
		case resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests:
			return fmt.Errorf("notify: webhook refused the notification: %s", resp.Status)
		}
		last = fmt.Errorf("notify: webhook answered %s", resp.Status)
	}
	return fmt.Errorf("notify: webhook failed after %d attempts: %v", attempts, last)
}
