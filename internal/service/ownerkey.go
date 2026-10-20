package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Owner keys (R3.6, owner decision 2026-10-20). While the owner still has
// SSH access, oapctl enrols a key it keeps on the owner's machine. A login
// as "owner" proving an enrolled key gets an owner token, so the owner
// keeps working over the public API after hand-off removes SSH access.
// Keys are enrolled and removed only over the local socket, so a stolen
// owner token can't add a key of its own; every owner sign-in is reported
// to the owner.

func localOwner(c Caller) error {
	if err := ownerOnly(c); err != nil {
		return err
	}
	if !c.Local {
		return fail(PermissionDenied, WhyLocalOnly, "owner keys are enrolled and removed only over SSH; use break-glass access if you have none")
	}
	return nil
}

// EnrollOwnerKey makes a key the owner's.
func (s *Service) EnrollOwnerKey(ctx context.Context, c Caller, req *oapv1.EnrollOwnerKeyRequest) (*oapv1.EnrollOwnerKeyResponse, error) {
	if err := localOwner(c); err != nil {
		return nil, err
	}
	if _, err := parseKey(req.GetPublicKey()); err != nil {
		return nil, fail(InvalidArgument, WhyBadKey, "%v", err)
	}
	label := strings.TrimSpace(req.GetLabel())
	if len(label) > 64 {
		return nil, fail(InvalidArgument, WhyBadKey, "the label is longer than 64 characters")
	}
	if err := s.record(ctx, idEvent{Event: "owner_key", Key: req.GetPublicKey(), Label: label}); err != nil {
		return nil, err
	}
	fp := Fingerprint(req.GetPublicKey())
	s.tellOwner(ctx, ops.Notification{Kind: "owner_key", Subject: "An owner key was enrolled",
		Body: fmt.Sprintf("Key %s (%s) can now sign in as the owner. If this wasn't you, remove it over SSH with: oapctl owner keys remove %s", fp, label, fp)})
	return &oapv1.EnrollOwnerKeyResponse{KeyFingerprint: fp}, nil
}

// ListOwnerKeys returns the owner's keys, oldest first.
func (s *Service) ListOwnerKeys(ctx context.Context, c Caller) (*oapv1.ListOwnerKeysResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	id := s.ids()
	id.mu.Lock()
	defer id.mu.Unlock()
	out := &oapv1.ListOwnerKeysResponse{}
	for key, k := range id.ownerKeys {
		out.Keys = append(out.Keys, &oapv1.OwnerKey{KeyFingerprint: Fingerprint(key), Label: k.label, EnrolledAt: timestamppb.New(k.enrolled)})
	}
	sort.Slice(out.Keys, func(i, j int) bool {
		a, b := out.Keys[i].EnrolledAt.AsTime(), out.Keys[j].EnrolledAt.AsTime()
		if a.Equal(b) {
			return out.Keys[i].KeyFingerprint < out.Keys[j].KeyFingerprint
		}
		return a.Before(b)
	})
	return out, nil
}

// RemoveOwnerKey removes an owner key and revokes its sessions.
func (s *Service) RemoveOwnerKey(ctx context.Context, c Caller, req *oapv1.RemoveOwnerKeyRequest) (*oapv1.RemoveOwnerKeyResponse, error) {
	if err := localOwner(c); err != nil {
		return nil, err
	}
	id := s.ids()
	id.mu.Lock()
	key := ""
	for k := range id.ownerKeys {
		if Fingerprint(k) == req.GetKeyFingerprint() {
			key = k
		}
	}
	active := 0
	now := s.Clock.Now()
	for _, t := range id.tokens {
		if t.agent == "owner" && t.key == key && key != "" && !t.revoked && now.Before(t.expires) {
			active++
		}
	}
	id.mu.Unlock()
	if key == "" {
		return nil, fail(NotFound, WhyUnknownKey, "no owner key %q", req.GetKeyFingerprint())
	}
	if err := s.record(ctx, idEvent{Event: "owner_key_removed", Key: key}); err != nil {
		return nil, err
	}
	return &oapv1.RemoveOwnerKeyResponse{RevokedTokens: int32(active)}, nil
}

// ownerLogin signs the owner in with an enrolled key. It never waits:
// there is nobody to approve the owner.
func (s *Service) ownerLogin(ctx context.Context, req *oapv1.LoginRequest, send func(*oapv1.LoginResponse) error) error {
	if req.GetPublicKey() == "" {
		return fail(InvalidArgument, WhyBadAgentName, "\"owner\" isn't an agent name; the owner signs in with an enrolled owner key (oapctl owner enroll, over SSH)")
	}
	key, err := parseKey(req.GetPublicKey())
	if err != nil {
		return fail(InvalidArgument, WhyBadKey, "%v", err)
	}
	id := s.ids()
	id.mu.Lock()
	_, enrolled := id.ownerKeys[req.GetPublicKey()]
	id.mu.Unlock()
	if !enrolled {
		return send(refused(WhyNotOwnerKey, "key %s isn't an owner key; enrol it over SSH with oapctl owner enroll", Fingerprint(req.GetPublicKey())))
	}
	if err := s.checkProof(req, key); err != nil {
		return send(refused(WhyBadProof, "the login doesn't prove the owner holds the key: %v", err))
	}
	token, exp, err := s.issueWith(ctx, "owner", req.GetPublicKey())
	if err != nil {
		return err
	}
	if err := s.record(ctx, idEvent{Event: "login", Agent: "owner", Key: req.GetPublicKey(), Auto: true}); err != nil {
		return err
	}
	fp := Fingerprint(req.GetPublicKey())
	s.tellOwner(ctx, ops.Notification{Kind: "login", Subject: "The owner signed in",
		Body: fmt.Sprintf("Owner key %s signed in over the network. If this wasn't you, remove the key over SSH with: oapctl owner keys remove %s", fp, fp)})
	return send(&oapv1.LoginResponse{State: &oapv1.LoginResponse_Approved{Approved: &oapv1.LoginApproved{
		Token: token, ExpiresInSeconds: int32(exp.Sub(s.Clock.Now()).Seconds()),
		Scope: &oapv1.AgentScopeSummary{Applications: []string{"*"}, May: []string{"*"}}}}})
}
