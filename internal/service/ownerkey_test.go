package service

import (
	"context"
	"testing"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

type notes struct{ got []ops.Notification }

func (n *notes) Notify(_ context.Context, x ops.Notification) error {
	n.got = append(n.got, x)
	return nil
}

// TestOwnerKey: a key enrolled over the local socket signs in as the owner,
// with every sign-in reported; unenrolled keys, bad proofs and agents
// naming themselves "owner" are refused; only the local owner enrols or
// removes keys; removing a key ends its sessions; and it all survives a
// restart.
func TestOwnerKey(t *testing.T) {
	r := newRig(t)
	n := &notes{}
	r.svc.Notifier = n
	k := newKey(t)

	// Before enrolment the key is refused, and an agent can't be "owner".
	res := login(t, r, k.request("owner", r.clock.Now(), nonce()), nil)
	if f := res[len(res)-1].GetRefused(); f == nil || f.Why != WhyNotOwnerKey {
		t.Fatalf("unenrolled owner key: %v", res)
	}
	if err := r.svc.Login(ctx, &oapv1.LoginRequest{Agent: "owner"}, nil); code(err) != InvalidArgument {
		t.Fatalf("owner login without a key: %v", err)
	}

	// Enrolment needs the owner on the local socket.
	enroll := &oapv1.EnrollOwnerKeyRequest{PublicKey: k.pub, Label: "laptop"}
	agentTok := token(t, login(t, r, &oapv1.LoginRequest{Agent: "a1"}, approveSite(t, r, false)))
	agent, _ := r.svc.Authenticate(ctx, agentTok)
	if _, err := r.svc.EnrollOwnerKey(ctx, agent, enroll); code(err) != PermissionDenied {
		t.Fatalf("agent enrolled an owner key: %v", err)
	}
	fp, err := r.svc.EnrollOwnerKey(ctx, Owner, enroll)
	if err != nil || fp.KeyFingerprint != Fingerprint(k.pub) {
		t.Fatalf("enroll: %v %v", fp, err)
	}

	// The key signs in as the owner, and the owner hears about it.
	before := len(n.got)
	tok := token(t, login(t, r, k.request("owner", r.clock.Now(), nonce()), nil))
	c, err := r.svc.Authenticate(ctx, tok)
	if err != nil || !c.Owner || c.Local || c.Agent != "owner" {
		t.Fatalf("owner session: %+v %v", c, err)
	}
	if len(n.got) != before+1 || n.got[len(n.got)-1].Kind != "login" {
		t.Fatalf("owner sign-in not reported: %v", n.got[before:])
	}
	if _, err := r.svc.ListAgents(ctx, c); err != nil {
		t.Fatalf("owner session can't use the owner API: %v", err)
	}
	// Pausing every agent doesn't pause the owner.
	r.svc.Pause(ctx, c, &oapv1.PauseRequest{})
	if _, err := r.svc.Authenticate(ctx, tok); err != nil {
		t.Fatalf("pause all stopped the owner: %v", err)
	}
	r.svc.Resume(ctx, c, &oapv1.ResumeRequest{})

	// A network owner session can't add or remove keys.
	other := newKey(t)
	if _, err := r.svc.EnrollOwnerKey(ctx, c, &oapv1.EnrollOwnerKeyRequest{PublicKey: other.pub}); code(err) != PermissionDenied {
		t.Fatalf("network owner session enrolled a key: %v", err)
	}
	if _, err := r.svc.RemoveOwnerKey(ctx, c, &oapv1.RemoveOwnerKeyRequest{KeyFingerprint: fp.KeyFingerprint}); code(err) != PermissionDenied {
		t.Fatalf("network owner session removed a key: %v", err)
	}

	// A forged proof for the enrolled key is refused.
	forged := k.request("owner", r.clock.Now(), nonce())
	forged.Signature = other.request("owner", r.clock.Now(), nonce()).Signature
	if res := login(t, r, forged, nil); res[len(res)-1].GetRefused().GetWhy() != WhyBadProof {
		t.Fatalf("forged owner proof: %v", res)
	}

	keys, err := r.svc.ListOwnerKeys(ctx, c)
	if err != nil || len(keys.Keys) != 1 || keys.Keys[0].Label != "laptop" {
		t.Fatalf("keys: %v %v", keys, err)
	}
	if _, err := r.svc.ListOwnerKeys(ctx, agent); code(err) != PermissionDenied {
		t.Fatalf("agent listed owner keys: %v", err)
	}

	// The state survives a restart.
	next := r.service()
	if err := next.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if c, err := next.Authenticate(ctx, tok); err != nil || !c.Owner {
		t.Fatalf("owner session after restart: %v", err)
	}

	// Removing the key ends its sessions and its sign-ins.
	rm, err := next.RemoveOwnerKey(ctx, Owner, &oapv1.RemoveOwnerKeyRequest{KeyFingerprint: fp.KeyFingerprint})
	if err != nil || rm.RevokedTokens != 1 {
		t.Fatalf("remove: %v %v", rm, err)
	}
	if _, err := next.Authenticate(ctx, tok); code(err) != Unauthenticated {
		t.Fatalf("owner session after removing its key: %v", err)
	}
	r.svc = next
	if res := login(t, r, k.request("owner", r.clock.Now(), nonce()), nil); res[len(res)-1].GetRefused().GetWhy() != WhyNotOwnerKey {
		t.Fatalf("removed key signed in: %v", res)
	}
}
