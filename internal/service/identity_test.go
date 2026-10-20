package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
)

// agentKey is an agent's identity key.
type agentKey struct {
	priv ed25519.PrivateKey
	pub  string
}

func newKey(t *testing.T) agentKey {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return agentKey{priv: priv, pub: "ed25519:" + base64.StdEncoding.EncodeToString(pub)}
}

// request builds a signed login request.
func (k agentKey) request(agent string, at time.Time, nonce string) *oapv1.LoginRequest {
	signedAt := at.UTC().Format(time.RFC3339)
	return &oapv1.LoginRequest{Agent: agent, PublicKey: k.pub, SignedAt: signedAt, Nonce: nonce,
		Signature: ed25519.Sign(k.priv, LoginPayload(agent, k.pub, signedAt, nonce))}
}

var nonceN int

func nonce() string {
	nonceN++
	return "nonce-for-test-" + string(rune('a'+nonceN%26)) + time.Now().Format("150405.000000000")
}

// login runs a login; approve, when set, is called with the code once the
// login is waiting. It returns every response.
func login(t *testing.T, r *rig, req *oapv1.LoginRequest, approve func(code string)) []*oapv1.LoginResponse {
	t.Helper()
	var out []*oapv1.LoginResponse
	done := make(chan error, 1)
	codes := make(chan string, 1)
	go func() {
		done <- r.svc.Login(ctx, req, func(res *oapv1.LoginResponse) error {
			out = append(out, res)
			if p := res.GetPending(); p != nil {
				codes <- p.UserCode
			}
			return nil
		})
	}()
	select {
	case code := <-codes:
		if approve != nil {
			approve(code)
		}
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return out
}

func token(t *testing.T, res []*oapv1.LoginResponse) string {
	t.Helper()
	a := res[len(res)-1].GetApproved()
	if a == nil {
		t.Fatalf("login not approved: %v", res)
	}
	return a.Token
}

func approveSite(t *testing.T, r *rig, trust bool) func(string) {
	return func(code string) {
		if l, _ := r.svc.ListLogins(ctx, Owner); len(l.Logins) != 1 || l.Logins[0].UserCode != code {
			t.Errorf("waiting logins: %v", l)
		}
		if _, err := r.svc.ApproveLogin(ctx, Owner, &oapv1.ApproveLoginRequest{UserCode: code, Applications: []string{"site"}, May: []string{"scale"}, Trust: trust}); err != nil {
			t.Error(err)
		}
	}
}

// TestDeviceLoginAndScope: the owner approves a login with a scope; the
// token works inside it and is refused outside it.
func TestDeviceLoginAndScope(t *testing.T) {
	r := newRig(t)
	r.create(t)
	res := login(t, r, &oapv1.LoginRequest{Agent: "claude-laptop"}, approveSite(t, r, false))
	if res[0].GetPending() == nil || len(res) != 2 {
		t.Fatalf("responses %v", res)
	}
	tok := token(t, res)
	c, err := r.svc.Authenticate(ctx, tok)
	if err != nil || c.Agent != "claude-laptop" || c.Owner {
		t.Fatalf("caller %+v %v", c, err)
	}
	if _, err := r.svc.Observe(ctx, c, &oapv1.ObserveRequest{Application: "site"}); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.Propose(ctx, c, &oapv1.ProposeRequest{Intent: scale("site", 2)})
	if err != nil || p.Proposal.Verdict != oapv1.Verdict_VERDICT_ALLOWED {
		t.Fatalf("scale within scope: %v %v", p, err)
	}
	for name, err := range map[string]error{
		"observe": func() error { _, err := r.svc.Observe(ctx, c, &oapv1.ObserveRequest{Application: "blog"}); return err }(),
		"propose": func() error {
			_, err := r.svc.Propose(ctx, c, &oapv1.ProposeRequest{Intent: scale("blog", 2)})
			return err
		}(),
		"explain":  func() error { _, err := r.svc.Explain(ctx, c, &oapv1.ExplainRequest{Application: "blog"}); return err }(),
		"watch":    r.svc.Watch(ctx, c, &oapv1.WatchRequest{Application: "blog"}, func(*oapv1.Event) error { return nil }),
		"owner op": func() error { _, err := r.svc.ListAgents(ctx, c); return err }(),
	} {
		if code(err) != PermissionDenied {
			t.Errorf("%s outside the scope: %v", name, err)
		}
	}
	if w, _ := r.svc.Observe(ctx, c, &oapv1.ObserveRequest{}); len(w.World.Applications) != 1 {
		t.Fatalf("agent sees %d applications", len(w.World.Applications))
	}
	if _, err := r.svc.Authenticate(ctx, tok+"x"); code(err) != Unauthenticated {
		t.Fatalf("unknown token: %v", err)
	}
}

// TestExpiredRevokedPausedTokens: expired and revoked tokens are refused;
// a pause takes effect on the next request and resume undoes it.
func TestExpiredRevokedPausedTokens(t *testing.T) {
	r := newRig(t)
	tok := token(t, login(t, r, &oapv1.LoginRequest{Agent: "a1"}, approveSite(t, r, false)))
	if _, err := r.svc.Authenticate(ctx, tok); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		pause, resume string
	}{{"a1", "a1"}, {"", ""}} {
		r.svc.Pause(ctx, Owner, &oapv1.PauseRequest{Agent: p.pause})
		if _, err := r.svc.Authenticate(ctx, tok); code(err) != PermissionDenied {
			t.Fatalf("paused (%q): %v", p.pause, err)
		}
		r.svc.Resume(ctx, Owner, &oapv1.ResumeRequest{Agent: p.resume})
		if _, err := r.svc.Authenticate(ctx, tok); err != nil {
			t.Fatalf("resumed: %v", err)
		}
	}
	r.clock.Advance(DefaultTokenTTL)
	if _, err := r.svc.Authenticate(ctx, tok); code(err) != Unauthenticated {
		t.Fatalf("expired: %v", err)
	}
	tok2 := token(t, login(t, r, &oapv1.LoginRequest{Agent: "a1"}, approveSite(t, r, false)))
	if rv, err := r.svc.Revoke(ctx, Owner, &oapv1.RevokeRequest{Agent: "a1"}); err != nil || rv.RevokedTokens != 1 {
		t.Fatalf("revoke: %v %v", rv, err)
	}
	_, err := r.svc.Authenticate(ctx, tok2)
	var e *Error
	if !errorsAs(err, &e) || e.Code != Unauthenticated || e.Detail.Why != WhyRevoked {
		t.Fatalf("revoked: %v", err)
	}
	// State survives a restart: the next service on the same journal agrees.
	next := r.service()
	if err := next.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Authenticate(ctx, tok2); code(err) != Unauthenticated {
		t.Fatalf("revoked token after restart: %v", err)
	}
}

// TestTrustedIdentity: after the owner trusts an agent's key, a login that
// proves the key is approved at once; bad, replayed or stale proofs are
// refused; "every session" mode and forgetting the identity bring the
// owner back in.
func TestTrustedIdentity(t *testing.T) {
	r := newRig(t)
	k := newKey(t)
	now := r.clock.Now()
	first := login(t, r, k.request("claude", now, nonce()), approveSite(t, r, true))
	token(t, first)
	if info, _ := r.svc.ListAgents(ctx, Owner); !info.Agents[0].Trusted || info.Mode != oapv1.ApprovalMode_APPROVAL_MODE_TRUSTED_IDENTITY {
		t.Fatalf("agents %v", info)
	}
	again := login(t, r, k.request("claude", now, nonce()), nil)
	if len(again) != 1 || again[0].GetApproved() == nil {
		t.Fatalf("trusted login asked the owner: %v", again)
	}
	n := nonce()
	login(t, r, k.request("claude", now, n), nil)
	if res := login(t, r, k.request("claude", now, n), nil); res[0].GetRefused().GetWhy() != WhyBadProof {
		t.Fatalf("replayed nonce: %v", res)
	}
	if res := login(t, r, k.request("claude", now.Add(-5*time.Minute), nonce()), nil); res[0].GetRefused().GetWhy() != WhyBadProof {
		t.Fatalf("stale proof: %v", res)
	}
	forged := k.request("claude", now, nonce())
	forged.Signature[0] ^= 0xff
	if res := login(t, r, forged, nil); res[0].GetRefused().GetWhy() != WhyBadProof {
		t.Fatalf("forged proof: %v", res)
	}
	other := newKey(t) // a different key under the trusted name waits for the owner
	if res := login(t, r, other.request("claude", now, nonce()), func(code string) {
		r.svc.DenyLogin(ctx, Owner, &oapv1.DenyLoginRequest{UserCode: code})
	}); res[len(res)-1].GetRefused().GetWhy() != WhyDenied {
		t.Fatalf("another key: %v", res)
	}
	r.svc.SetApprovalMode(ctx, Owner, &oapv1.SetApprovalModeRequest{Mode: oapv1.ApprovalMode_APPROVAL_MODE_EVERY_SESSION})
	if res := login(t, r, k.request("claude", now, nonce()), approveSite(t, r, true)); res[0].GetPending() == nil {
		t.Fatalf("every-session mode didn't ask: %v", res)
	}
	r.svc.SetApprovalMode(ctx, Owner, &oapv1.SetApprovalModeRequest{Mode: oapv1.ApprovalMode_APPROVAL_MODE_TRUSTED_IDENTITY})
	r.svc.Revoke(ctx, Owner, &oapv1.RevokeRequest{Agent: "claude", ForgetIdentity: true})
	if res := login(t, r, k.request("claude", now, nonce()), approveSite(t, r, false)); res[0].GetPending() == nil {
		t.Fatalf("forgotten identity didn't ask: %v", res)
	}
}

func TestPendingLoginExpires(t *testing.T) {
	r := newRig(t)
	r.svc.LoginTTL = 50 * time.Millisecond
	res := login(t, r, &oapv1.LoginRequest{Agent: "slow"}, func(string) {})
	if res[len(res)-1].GetRefused().GetWhy() != WhyLoginExpired {
		t.Fatalf("responses %v", res)
	}
	if _, err := r.svc.ApproveLogin(ctx, Owner, &oapv1.ApproveLoginRequest{UserCode: res[0].GetPending().UserCode, Applications: []string{"*"}}); code(err) != NotFound {
		t.Fatalf("approving an expired login: %v", err)
	}
	if err := r.svc.Login(ctx, &oapv1.LoginRequest{Agent: "owner"}, nil); code(err) != InvalidArgument {
		t.Fatalf("agent named owner: %v", err)
	}
}

func errorsAs(err error, target **Error) bool { return errors.As(err, target) }
