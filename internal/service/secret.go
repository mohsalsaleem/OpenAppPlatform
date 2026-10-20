package service

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

// Owner secrets (R3.15). The owner stores values; specs reference them by
// name, and the runtime resolves them when it starts a container. A value
// is never returned, logged, journaled or committed: the journal records
// only that a named secret was set or deleted, and by whom. Agents can't
// set, list or read secrets; they ask the owner for one with a secret ask.

// KindSecret is the journal entry kind for secret changes.
const KindSecret = "secret"

// Why codes for secrets.
const (
	WhyBadSecret     = "secret.bad_request"
	WhyNoSecretStore = "secret.no_store"
	WhyUnknownSecret = "secret.unknown"
)

// MaxSecretSize is the largest value a secret holds.
const MaxSecretSize = 64 << 10

// The names specs may reference (the application schema's slug).
var secretName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type secretEvent struct {
	Event string `json:"event"` // set, delete
	Name  string `json:"name"`
	Ask   string `json:"ask,omitempty"`
}

func (s *Service) secretStore() (ports.SecretStore, error) {
	if s.SecretStore == nil {
		return nil, fail(FailedPrecondition, WhyNoSecretStore, "this server has no secret store")
	}
	return s.SecretStore, nil
}

func (s *Service) recordSecret(ctx context.Context, ev secretEvent) error {
	data, _ := json.Marshal(ev)
	_, err := s.Journal.Append(ctx, ops.Entry{At: s.Clock.Now(), Kind: KindSecret, Data: data})
	return err
}

// SetSecret stores a secret value and, with ask_id, answers that secret
// ask with "provided".
func (s *Service) SetSecret(ctx context.Context, c Caller, req *oapv1.SetSecretRequest) (*oapv1.SetSecretResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	st, err := s.secretStore()
	if err != nil {
		return nil, err
	}
	name := req.GetName()
	switch {
	case !secretName.MatchString(name):
		return nil, fail(InvalidArgument, WhyBadSecret, "secret names are lowercase letters, digits and dashes, as specs reference them")
	case req.GetValue() == "":
		return nil, fail(InvalidArgument, WhyBadSecret, "the value is empty")
	case len(req.GetValue()) > MaxSecretSize:
		return nil, fail(InvalidArgument, WhyBadSecret, "the value is larger than 64 KiB")
	}
	var ask *askState
	if id := req.GetAskId(); id != "" {
		b := s.book()
		b.mu.Lock()
		a, ok := b.asks[id]
		b.mu.Unlock()
		switch {
		case !ok:
			return nil, fail(NotFound, WhyUnknownAsk, "no ask %q", id)
		case a.req.GetKind() != oapv1.AskKind_ASK_KIND_SECRET:
			return nil, fail(InvalidArgument, WhyBadAnswer, "ask %s isn't a secret ask", id)
		case a.req.GetSecretRef() != name:
			return nil, fail(InvalidArgument, WhyBadAnswer, "ask %s is for secret %q, not %q", id, a.req.GetSecretRef(), name)
		}
		ask = a
	}
	if err := st.Set(name, req.GetValue()); err != nil {
		return nil, fail(Internal, WhyBadSecret, "storing the secret failed") // the store's error could name more than we want
	}
	if err := s.recordSecret(ctx, secretEvent{Event: "set", Name: name, Ask: req.GetAskId()}); err != nil {
		return nil, err
	}
	if ask != nil {
		if _, err := s.AnswerAsk(ctx, c, &oapv1.AnswerAskRequest{AskId: ask.id, Answer: "provided"}); err != nil {
			return nil, err
		}
	}
	return &oapv1.SetSecretResponse{}, nil
}

// ListSecrets returns the secrets' names.
func (s *Service) ListSecrets(ctx context.Context, c Caller) (*oapv1.ListSecretsResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	st, err := s.secretStore()
	if err != nil {
		return nil, err
	}
	names, err := st.Names()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return &oapv1.ListSecretsResponse{Names: names}, nil
}

// DeleteSecret removes a secret.
func (s *Service) DeleteSecret(ctx context.Context, c Caller, req *oapv1.DeleteSecretRequest) (*oapv1.DeleteSecretResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	st, err := s.secretStore()
	if err != nil {
		return nil, err
	}
	if !s.hasSecret(req.GetName()) {
		return nil, fail(NotFound, WhyUnknownSecret, "no secret %q", req.GetName())
	}
	if err := st.Delete(req.GetName()); err != nil {
		return nil, err
	}
	return &oapv1.DeleteSecretResponse{}, s.recordSecret(ctx, secretEvent{Event: "delete", Name: req.GetName()})
}

// hasSecret reports whether a secret is set.
func (s *Service) hasSecret(name string) bool {
	if s.SecretStore == nil {
		return false
	}
	names, err := s.SecretStore.Names()
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
