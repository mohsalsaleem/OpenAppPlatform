package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// Identity (R3.6). Agents sign in with a device login: they ask, the owner
// approves with a scope, and they receive a short-lived token. With trusted
// identity (the default, owner decision 2026-10-20) the owner may trust an
// agent's key at approval; later logins proving that key are approved
// without asking, and recorded. Tokens are kept only as hashes. Every
// change is a journal entry, so the state survives restarts.

// KindIdentity is the journal entry kind for identity events.
const KindIdentity = "identity"

// Why codes for identity errors.
const (
	WhyUnknownToken  = "identity.unknown_token"
	WhyRevoked       = "identity.revoked"
	WhyExpired       = "identity.expired"
	WhyPaused        = "identity.paused"
	WhyBadAgentName  = "identity.bad_agent_name"
	WhyBadKey        = "identity.bad_key"
	WhyBadProof      = "identity.bad_proof"
	WhyDenied        = "identity.denied"
	WhyLoginExpired  = "identity.login_expired"
	WhyUnknownLogin  = "identity.unknown_login"
	WhyUnknownAgent  = "identity.unknown_agent"
	WhyOwnerOnly     = "identity.owner_only"
	WhyTooManyLogins = "identity.too_many_logins"
	WhyLocalOnly     = "identity.local_only"
	WhyNotOwnerKey   = "identity.not_owner_key"
	WhyUnknownKey    = "identity.unknown_key"
)

// Identity settings with their defaults.
const (
	DefaultTokenTTL = time.Hour
	DefaultLoginTTL = 10 * time.Minute
	proofWindow     = 2 * time.Minute
	// MaxPendingLogins bounds the logins waiting for the owner: anyone can
	// start one on the public listener, without a token.
	MaxPendingLogins = 16
	// maxNonces bounds the proof nonces remembered within the window.
	maxNonces = 4096
)

var agentName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

type idEvent struct {
	Event   string   `json:"event"` // approve, token, revoke, pause, resume, mode, login
	Agent   string   `json:"agent,omitempty"`
	Apps    []string `json:"apps,omitempty"`
	May     []string `json:"may,omitempty"`
	Trust   bool     `json:"trust,omitempty"`
	Key     string   `json:"key,omitempty"`
	TokenID string   `json:"token_id,omitempty"`
	Hash    string   `json:"hash,omitempty"`
	Expires string   `json:"expires,omitempty"`
	Forget  bool     `json:"forget,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	Auto    bool     `json:"auto,omitempty"`
	Label   string   `json:"label,omitempty"`
}

type agentState struct {
	apps, may []string
	trusted   bool
	key       string
	paused    bool
}

type tokenState struct {
	id, agent string
	key       string // the owner key that signed this owner session in
	expires   time.Time
	revoked   bool
}

type ownerKey struct {
	label    string
	enrolled time.Time
}

type pendingLogin struct {
	code, agent, key string
	expires          time.Time
	done             chan loginResult
}

type loginResult struct {
	approved bool
	reason   string
}

type identity struct {
	mu        sync.Mutex
	agents    map[string]*agentState
	tokens    map[string]*tokenState // by hash
	allPaused bool
	mode      oapv1.ApprovalMode
	pending   map[string]*pendingLogin
	nonces    map[string]time.Time
	ownerKeys map[string]*ownerKey // by public key
}

func (s *Service) ids() *identity {
	s.idOnce.Do(func() {
		s.id = &identity{agents: map[string]*agentState{}, tokens: map[string]*tokenState{},
			mode: oapv1.ApprovalMode_APPROVAL_MODE_TRUSTED_IDENTITY, pending: map[string]*pendingLogin{}, nonces: map[string]time.Time{},
			ownerKeys: map[string]*ownerKey{}}
	})
	return s.id
}

// loadIdentity replays identity events from the journal.
func (s *Service) loadIdentity(entries []ops.Entry) {
	id := s.ids()
	id.mu.Lock()
	defer id.mu.Unlock()
	for _, e := range entries {
		if e.Kind != KindIdentity {
			continue
		}
		var ev idEvent
		if json.Unmarshal(e.Data, &ev) == nil {
			id.apply(ev, e.At)
		}
	}
}

func (id *identity) apply(ev idEvent, at time.Time) {
	switch ev.Event {
	case "owner_key":
		id.ownerKeys[ev.Key] = &ownerKey{label: ev.Label, enrolled: at}
	case "owner_key_removed":
		delete(id.ownerKeys, ev.Key)
		for _, t := range id.tokens {
			if t.agent == "owner" && t.key == ev.Key {
				t.revoked = true
			}
		}
	case "approve":
		a := &agentState{apps: ev.Apps, may: ev.May, trusted: ev.Trust, key: ev.Key}
		if old, ok := id.agents[ev.Agent]; ok {
			a.paused = old.paused
		}
		id.agents[ev.Agent] = a
	case "token":
		exp, _ := time.Parse(time.RFC3339, ev.Expires)
		id.tokens[ev.Hash] = &tokenState{id: ev.TokenID, agent: ev.Agent, key: ev.Key, expires: exp}
	case "revoke":
		for _, t := range id.tokens {
			if t.agent == ev.Agent {
				t.revoked = true
			}
		}
		if a, ok := id.agents[ev.Agent]; ok && ev.Forget {
			a.trusted, a.key = false, ""
		}
	case "pause", "resume":
		on := ev.Event == "pause"
		if ev.Agent == "" {
			id.allPaused = on
		} else if a, ok := id.agents[ev.Agent]; ok {
			a.paused = on
		}
	case "mode":
		if m, ok := oapv1.ApprovalMode_value[ev.Mode]; ok {
			id.mode = oapv1.ApprovalMode(m)
		}
	}
}

// record appends an identity event and applies it.
func (s *Service) record(ctx context.Context, ev idEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	if _, err := s.Journal.Append(ctx, ops.Entry{At: now, Kind: KindIdentity, Data: data}); err != nil {
		return err
	}
	id := s.ids()
	id.mu.Lock()
	id.apply(ev, now)
	id.mu.Unlock()
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Authenticate turns a bearer token into a caller. Expired, revoked and
// unknown tokens are refused; a paused agent is refused from its next
// request on.
func (s *Service) Authenticate(ctx context.Context, token string) (Caller, error) {
	id := s.ids()
	id.mu.Lock()
	defer id.mu.Unlock()
	t, ok := id.tokens[hashToken(token)]
	switch {
	case !ok:
		return Caller{}, fail(Unauthenticated, WhyUnknownToken, "the token isn't one OAP issued; sign in again")
	case t.revoked:
		return Caller{}, fail(Unauthenticated, WhyRevoked, "the owner revoked this token; sign in again")
	case !s.Clock.Now().Before(t.expires):
		return Caller{}, fail(Unauthenticated, WhyExpired, "the token expired at %s; sign in again", t.expires.Format(time.RFC3339))
	}
	if t.agent == "owner" {
		return Caller{Owner: true, Agent: "owner"}, nil
	}
	a, ok := id.agents[t.agent]
	if !ok {
		return Caller{}, fail(Unauthenticated, WhyUnknownAgent, "agent %s isn't approved", t.agent)
	}
	if id.allPaused || a.paused {
		return Caller{}, fail(PermissionDenied, WhyPaused, "the owner paused agent %s", t.agent)
	}
	return Caller{Agent: t.agent, Apps: append([]string(nil), a.apps...)}, nil
}

// issue creates a token for an agent.
func (s *Service) issue(ctx context.Context, agent string) (string, time.Time, error) {
	return s.issueWith(ctx, agent, "")
}

// issueWith creates a token, remembering the owner key that signed in.
func (s *Service) issueWith(ctx context.Context, agent, key string) (string, time.Time, error) {
	token := "oapt_" + randomString(32)
	exp := s.Clock.Now().Add(DefaultTokenTTL).UTC().Truncate(time.Second)
	err := s.record(ctx, idEvent{Event: "token", Agent: agent, Key: key, TokenID: "tok_" + randomString(6), Hash: hashToken(token), Expires: exp.Format(time.RFC3339)})
	return token, exp, err
}

// parseKey reads "ed25519:<base64>".
func parseKey(s string) (ed25519.PublicKey, error) {
	b64, ok := strings.CutPrefix(s, "ed25519:")
	raw, err := base64.StdEncoding.DecodeString(b64)
	if !ok || err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public keys are \"ed25519:\" and a base64 32-byte key")
	}
	return ed25519.PublicKey(raw), nil
}

// Fingerprint names a public key.
func Fingerprint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// LoginPayload is what an agent signs to prove it holds its key.
func LoginPayload(agent, key, signedAt, nonce string) []byte {
	return []byte("oap-login-v1\n" + agent + "\n" + key + "\n" + signedAt + "\n" + nonce)
}

// checkProof verifies a login's signature, freshness and nonce.
func (s *Service) checkProof(req *oapv1.LoginRequest, key ed25519.PublicKey) error {
	at, err := time.Parse(time.RFC3339, req.GetSignedAt())
	now := s.Clock.Now()
	switch {
	case len(req.GetSignature()) == 0:
		return fmt.Errorf("no signature")
	case err != nil:
		return fmt.Errorf("signed_at isn't RFC 3339")
	case at.Before(now.Add(-proofWindow)) || at.After(now.Add(proofWindow)):
		return fmt.Errorf("the signature is from %s, more than two minutes from now", at.Format(time.RFC3339))
	case len(req.GetNonce()) < 16:
		return fmt.Errorf("the nonce is too short")
	case !ed25519.Verify(key, LoginPayload(req.GetAgent(), req.GetPublicKey(), req.GetSignedAt(), req.GetNonce()), req.GetSignature()):
		return fmt.Errorf("the signature doesn't match the key")
	}
	id := s.ids()
	id.mu.Lock()
	defer id.mu.Unlock()
	for n, t := range id.nonces { // forget nonces older than the window
		if now.Sub(t) > 2*proofWindow {
			delete(id.nonces, n)
		}
	}
	if _, used := id.nonces[req.GetNonce()]; used {
		return fmt.Errorf("the nonce was already used")
	}
	if len(id.nonces) >= maxNonces {
		return fmt.Errorf("too many sign-ins in the last few minutes; try again shortly")
	}
	id.nonces[req.GetNonce()] = now
	return nil
}

func scopeSummary(a *agentState) *oapv1.AgentScopeSummary {
	return &oapv1.AgentScopeSummary{Applications: a.apps, May: a.may}
}

func refused(why, format string, args ...any) *oapv1.LoginResponse {
	return &oapv1.LoginResponse{State: &oapv1.LoginResponse_Refused{Refused: &oapv1.Reason{Why: why, Message: fmt.Sprintf(format, args...)}}}
}

// Login runs a device login. An agent whose key the owner trusted, proving
// it holds that key, is approved at once (trusted identity mode); otherwise
// the first event carries a code for the owner, and the login waits for
// the owner's answer or its expiry.
func (s *Service) Login(ctx context.Context, req *oapv1.LoginRequest, send func(*oapv1.LoginResponse) error) error {
	agent := req.GetAgent()
	if agent == "owner" {
		return s.ownerLogin(ctx, req, send)
	}
	if !agentName.MatchString(agent) {
		return fail(InvalidArgument, WhyBadAgentName, "agent names are lowercase letters, digits, dots, dashes and underscores, and not \"owner\"")
	}
	var key ed25519.PublicKey
	if req.GetPublicKey() != "" {
		k, err := parseKey(req.GetPublicKey())
		if err != nil {
			return fail(InvalidArgument, WhyBadKey, "%v", err)
		}
		if err := s.checkProof(req, k); err != nil {
			return send(refused(WhyBadProof, "the login doesn't prove the agent holds its key: %v", err))
		}
		key = k
	}
	id := s.ids()
	id.mu.Lock()
	a, known := id.agents[agent]
	trusted := known && a.trusted && key != nil && a.key == req.GetPublicKey() && id.mode == oapv1.ApprovalMode_APPROVAL_MODE_TRUSTED_IDENTITY
	id.mu.Unlock()
	if trusted {
		token, exp, err := s.issue(ctx, agent)
		if err != nil {
			return err
		}
		if err := s.record(ctx, idEvent{Event: "login", Agent: agent, Auto: true}); err != nil {
			return err
		}
		s.tellOwner(ctx, ops.Notification{Kind: "login", Subject: agent + " signed in with its trusted identity",
			Body: fmt.Sprintf("Key %s. Pause it with: oapctl owner pause %s; revoke with: oapctl owner revoke %s --forget", Fingerprint(req.GetPublicKey()), agent, agent)})
		return send(&oapv1.LoginResponse{State: &oapv1.LoginResponse_Approved{Approved: &oapv1.LoginApproved{
			Token: token, ExpiresInSeconds: int32(exp.Sub(s.Clock.Now()).Seconds()), Scope: scopeSummary(a)}}})
	}

	p := &pendingLogin{code: userCode(), agent: agent, key: req.GetPublicKey(), expires: s.Clock.Now().Add(s.loginTTL()), done: make(chan loginResult, 1)}
	id.mu.Lock()
	if len(id.pending) >= MaxPendingLogins {
		id.mu.Unlock()
		return send(refused(WhyTooManyLogins, "%d logins are already waiting for the owner; try again once they are answered or expire", MaxPendingLogins))
	}
	id.pending[p.code] = p
	id.mu.Unlock()
	defer func() {
		id.mu.Lock()
		delete(id.pending, p.code)
		id.mu.Unlock()
	}()
	if err := send(&oapv1.LoginResponse{State: &oapv1.LoginResponse_Pending{Pending: &oapv1.LoginPending{
		UserCode: p.code, VerificationUrl: "oapctl owner approve " + p.code, ExpiresInSeconds: int32(s.loginTTL().Seconds())}}}); err != nil {
		return err
	}
	timer := time.NewTimer(s.loginTTL())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return send(refused(WhyLoginExpired, "the owner didn't answer within %s; log in again", s.loginTTL()))
	case r := <-p.done:
		if !r.approved {
			return send(refused(WhyDenied, "the owner denied this login"))
		}
	}
	token, exp, err := s.issue(ctx, agent)
	if err != nil {
		return err
	}
	id.mu.Lock()
	a = id.agents[agent]
	id.mu.Unlock()
	return send(&oapv1.LoginResponse{State: &oapv1.LoginResponse_Approved{Approved: &oapv1.LoginApproved{
		Token: token, ExpiresInSeconds: int32(exp.Sub(s.Clock.Now()).Seconds()), Scope: scopeSummary(a)}}})
}

func (s *Service) loginTTL() time.Duration {
	if s.LoginTTL > 0 {
		return s.LoginTTL
	}
	return DefaultLoginTTL
}

// userCode is a code the owner reads and types: eight characters from an
// alphabet without look-alikes, as XXXX-XXXX.
func userCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:])
}

func normalizeCode(c string) string {
	c = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(c), "-", ""))
	if len(c) == 8 {
		return c[:4] + "-" + c[4:]
	}
	return c
}

func ownerOnly(c Caller) error {
	if !c.Owner {
		return fail(PermissionDenied, WhyOwnerOnly, "only the owner may do this")
	}
	return nil
}

// ListLogins returns the logins waiting for the owner.
func (s *Service) ListLogins(ctx context.Context, c Caller) (*oapv1.ListLoginsResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	id := s.ids()
	id.mu.Lock()
	defer id.mu.Unlock()
	out := &oapv1.ListLoginsResponse{}
	for _, p := range id.pending {
		out.Logins = append(out.Logins, &oapv1.PendingLogin{UserCode: p.code, Agent: p.agent, KeyFingerprint: Fingerprint(p.key)})
	}
	sort.Slice(out.Logins, func(i, j int) bool { return out.Logins[i].UserCode < out.Logins[j].UserCode })
	return out, nil
}

// ApproveLogin approves a waiting login with a scope, writes the agent's
// policy into the envelope, and optionally trusts its key.
func (s *Service) ApproveLogin(ctx context.Context, c Caller, req *oapv1.ApproveLoginRequest) (*oapv1.ApproveLoginResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	id := s.ids()
	id.mu.Lock()
	p, ok := id.pending[normalizeCode(req.GetUserCode())]
	id.mu.Unlock()
	if !ok {
		return nil, fail(NotFound, WhyUnknownLogin, "no login is waiting with code %s", req.GetUserCode())
	}
	apps, may := req.GetApplications(), req.GetMay()
	if len(apps) == 0 {
		return nil, fail(InvalidArgument, intentIncomplete, "name the applications the agent may act on, or \"*\"")
	}
	if err := s.grantInEnvelope(ctx, p.agent, apps, may); err != nil {
		return nil, err
	}
	trust := req.GetTrust() && p.key != ""
	if err := s.record(ctx, idEvent{Event: "approve", Agent: p.agent, Apps: apps, May: may, Trust: trust, Key: p.key}); err != nil {
		return nil, err
	}
	p.done <- loginResult{approved: true}
	return &oapv1.ApproveLoginResponse{Agent: s.agentInfo(p.agent)}, nil
}

const intentIncomplete = "intent.incomplete"

// grantInEnvelope writes an agent's scope and actions into the envelope,
// where the policy engine judges its proposals.
func (s *Service) grantInEnvelope(ctx context.Context, agent string, apps, may []string) error {
	tree, _, err := s.Desired.Envelope(ctx)
	if err != nil {
		return err
	}
	env, _ := spec.Clone(tree).(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	agents, _ := env["agents"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
	}
	toAny := func(ss []string) []any {
		out := make([]any, len(ss))
		for i, s := range ss {
			out[i] = s
		}
		return out
	}
	agents[agent] = map[string]any{"scope": map[string]any{"applications": toAny(apps)}, "may": toAny(may)}
	env["agents"] = agents
	_, err = s.Desired.SetEnvelope(ctx, env, "owner", "Approve agent "+agent)
	return err
}

// DenyLogin refuses a waiting login.
func (s *Service) DenyLogin(ctx context.Context, c Caller, req *oapv1.DenyLoginRequest) (*oapv1.DenyLoginResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	id := s.ids()
	id.mu.Lock()
	p, ok := id.pending[normalizeCode(req.GetUserCode())]
	id.mu.Unlock()
	if !ok {
		return nil, fail(NotFound, WhyUnknownLogin, "no login is waiting with code %s", req.GetUserCode())
	}
	p.done <- loginResult{}
	return &oapv1.DenyLoginResponse{}, nil
}

func (s *Service) agentInfo(name string) *oapv1.AgentInfo {
	id := s.ids()
	id.mu.Lock()
	defer id.mu.Unlock()
	a, ok := id.agents[name]
	if !ok {
		return nil
	}
	active := 0
	now := s.Clock.Now()
	for _, t := range id.tokens {
		if t.agent == name && !t.revoked && now.Before(t.expires) {
			active++
		}
	}
	return &oapv1.AgentInfo{Name: name, Applications: a.apps, May: a.may, Trusted: a.trusted, KeyFingerprint: Fingerprint(a.key),
		Paused: a.paused || id.allPaused, ActiveTokens: int32(active)}
}

// ListAgents returns approved agents and the approval mode.
func (s *Service) ListAgents(ctx context.Context, c Caller) (*oapv1.ListAgentsResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	id := s.ids()
	id.mu.Lock()
	names := make([]string, 0, len(id.agents))
	for n := range id.agents {
		names = append(names, n)
	}
	mode, all := id.mode, id.allPaused
	id.mu.Unlock()
	sort.Strings(names)
	out := &oapv1.ListAgentsResponse{Mode: mode, AllPaused: all}
	for _, n := range names {
		out.Agents = append(out.Agents, s.agentInfo(n))
	}
	return out, nil
}

// Revoke ends an agent's tokens, and with forget_identity its trust.
func (s *Service) Revoke(ctx context.Context, c Caller, req *oapv1.RevokeRequest) (*oapv1.RevokeResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	info := s.agentInfo(req.GetAgent())
	if info == nil {
		return nil, fail(NotFound, WhyUnknownAgent, "no agent %q", req.GetAgent())
	}
	if err := s.record(ctx, idEvent{Event: "revoke", Agent: req.GetAgent(), Forget: req.GetForgetIdentity()}); err != nil {
		return nil, err
	}
	return &oapv1.RevokeResponse{RevokedTokens: info.ActiveTokens}, nil
}

// Pause stops an agent, or all agents, from the next request on.
func (s *Service) Pause(ctx context.Context, c Caller, req *oapv1.PauseRequest) (*oapv1.PauseResponse, error) {
	return &oapv1.PauseResponse{}, s.pauseOrResume(ctx, c, "pause", req.GetAgent())
}

// Resume undoes a pause.
func (s *Service) Resume(ctx context.Context, c Caller, req *oapv1.ResumeRequest) (*oapv1.ResumeResponse, error) {
	return &oapv1.ResumeResponse{}, s.pauseOrResume(ctx, c, "resume", req.GetAgent())
}

func (s *Service) pauseOrResume(ctx context.Context, c Caller, event, agent string) error {
	if err := ownerOnly(c); err != nil {
		return err
	}
	if agent != "" && s.agentInfo(agent) == nil {
		return fail(NotFound, WhyUnknownAgent, "no agent %q", agent)
	}
	return s.record(ctx, idEvent{Event: event, Agent: agent})
}

// SetApprovalMode chooses how later sessions are approved.
func (s *Service) SetApprovalMode(ctx context.Context, c Caller, req *oapv1.SetApprovalModeRequest) (*oapv1.SetApprovalModeResponse, error) {
	if err := ownerOnly(c); err != nil {
		return nil, err
	}
	if req.GetMode() == oapv1.ApprovalMode_APPROVAL_MODE_UNSPECIFIED {
		return nil, fail(InvalidArgument, intentIncomplete, "choose an approval mode")
	}
	return &oapv1.SetApprovalModeResponse{}, s.record(ctx, idEvent{Event: "mode", Mode: req.GetMode().String()})
}
