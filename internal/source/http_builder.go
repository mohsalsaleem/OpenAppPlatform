package source

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPBuilder delegates to an explicitly trusted private build host. Redirects
// are forbidden so a host response cannot forward its bearer credential.
type HTTPBuilder struct{ URL, Token string }

func (b HTTPBuilder) Build(ctx context.Context, r BuildRequest) (BuildResult, error) {
	var result BuildResult
	if b.URL == "" || len(b.Token) < 24 {
		return result, ErrUncertain
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	raw, _ := json.Marshal(r)
	request, e := http.NewRequestWithContext(ctx, "POST", b.URL, bytes.NewReader(raw))
	if e != nil {
		return result, ErrUncertain
	}
	request.Header.Set("Authorization", "Bearer "+b.Token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return result, ErrUncertain
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, ErrUncertain
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, 8193))
	if e != nil || len(raw) > 8192 {
		return result, ErrUncertain
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil {
		return result, ErrUncertain
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !ValidResult(r, result) {
		return result, ErrUncertain
	}
	return result, nil
}
