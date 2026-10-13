// Package access owns the single-workspace identity and credential foundation.
package access

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

var ErrDenied = errors.New("access denied")

type Service struct{ Pool *pgxpool.Pool }

const iterations = 600000

func Secret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func Password(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", errors.New("password must contain 12 to 256 bytes")
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$600000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func Matches(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != "600000" || len(password) > 256 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[2])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(parts[3])
	if e != nil || len(want) != 32 {
		return false
	}
	actual, e := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	return e == nil && subtle.ConstantTimeCompare(actual, want) == 1
}
func Email(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, e := mail.ParseAddress(email)
	if e != nil || a.Address != email || len(email) > 254 {
		return "", errors.New("provide a valid email address")
	}
	return email, nil
}
func (s Service) HasOwner(ctx context.Context) (bool, error) {
	var yes bool
	e := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_users WHERE role='owner')").Scan(&yes)
	return yes, e
}
func (s Service) Register(ctx context.Context, email, name, password, invite string) (string, error) {
	email, e := Email(email)
	if e != nil {
		return "", e
	}
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 80 {
		return "", errors.New("name must contain 1 to 80 bytes")
	}
	hash, e := Password(password)
	if e != nil {
		return "", e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(982014)"); e != nil {
		return "", e
	}
	role := "owner"
	if invite == "" {
		var exists bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_users WHERE role='owner')").Scan(&exists); e != nil {
			return "", e
		}
		if exists {
			return "", domain.ErrConflict
		}
	} else {
		var invitation string
		if e = tx.QueryRow(ctx, "SELECT id,role FROM oap_invitations WHERE token_hash=$1 AND email=$2 AND accepted_at IS NULL AND expires_at>now() FOR UPDATE", Hash(invite), email).Scan(&invitation, &role); e != nil {
			return "", ErrDenied
		}
		if _, e = tx.Exec(ctx, "UPDATE oap_invitations SET accepted_at=now() WHERE id=$1", invitation); e != nil {
			return "", e
		}
	}
	id := domain.NewID()
	if _, e = tx.Exec(ctx, "INSERT INTO oap_users(id,email,name,password_hash,role) VALUES($1,$2,$3,$4,$5)", id, email, name, hash, role); e != nil {
		return "", domain.ErrConflict
	}
	if _, e = tx.Exec(ctx, "INSERT INTO oap_audit(id,user_id,action,status,completed_at) VALUES($1,$2,$3,201,now())", domain.NewID(), id, "account.register."+role); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}
func (s Service) Login(ctx context.Context, email, password string) (string, error) {
	email, _ = Email(email)
	var id, hash string
	e := s.Pool.QueryRow(ctx, "SELECT id,password_hash FROM oap_users WHERE email=$1 AND active", email).Scan(&id, &hash)
	// Unknown accounts perform the same expensive KDF to reduce enumeration.
	if e != nil {
		hash = "pbkdf2-sha256$600000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	if !Matches(hash, password) || e != nil {
		return "", ErrDenied
	}
	return id, nil
}
func (s Service) Issue(ctx context.Context, user, kind, name, scope, app string, ttl time.Duration) (string, error) {
	return s.issue(ctx, user, kind, name, scope, app, ttl, nil)
}

// IssueRestricted is used for new owner-issued agents; legacy/internal callers remain stable.
func (s Service) IssueRestricted(ctx context.Context, user, name, scope, app string, ttl time.Duration, permissions domain.AgentPermissions) (string, error) {
	if permissions.TargetID == "" || permissions.Environment == "" || permissions.Actions == nil || (scope == "operate" && len(permissions.Actions) == 0) || (scope == "read" && len(permissions.Actions) > 0) {
		return "", ErrDenied
	}
	var target, environment string
	if err := s.Pool.QueryRow(ctx, "SELECT spec->>'targetId',environment FROM oap_applications WHERE id=$1 AND workspace_id='default'", app).Scan(&target, &environment); err != nil || target != permissions.TargetID || environment != permissions.Environment {
		return "", ErrDenied
	}
	seen := map[string]bool{}
	for _, action := range permissions.Actions {
		if !slices.Contains(domain.AgentActions, action) || seen[action] {
			return "", ErrDenied
		}
		seen[action] = true
	}
	return s.issue(ctx, user, "agent", name, scope, app, ttl, &permissions)
}
func (s Service) issue(ctx context.Context, user, kind, name, scope, app string, ttl time.Duration, permissions *domain.AgentPermissions) (string, error) {
	if (kind != "session" && kind != "agent") || (scope != "read" && scope != "operate") || len(name) < 1 || len(name) > 80 || ttl <= 0 || ttl > 30*24*time.Hour {
		return "", ErrDenied
	}
	secret := Secret()
	var raw []byte
	if permissions != nil {
		raw, _ = json.Marshal(permissions)
	}
	_, e := s.Pool.Exec(ctx, "INSERT INTO oap_credentials(id,user_id,token_hash,kind,name,scope,application_id,expires_at,permissions) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9)", domain.NewID(), user, Hash(secret), kind, name, scope, app, time.Now().Add(ttl), raw)
	return secret, e
}
func (s Service) Principal(ctx context.Context, secret string) (domain.Principal, error) {
	if len(secret) != 43 {
		return domain.Principal{}, ErrDenied
	}
	return s.principal(ctx, "c.token_hash=$1", Hash(secret))
}
func (s Service) ByID(ctx context.Context, id string) (domain.Principal, error) {
	return s.principal(ctx, "c.id=$1", id)
}
func (s Service) principal(ctx context.Context, where, arg string) (domain.Principal, error) {
	var p domain.Principal
	var raw []byte
	e := s.Pool.QueryRow(ctx, "SELECT u.id,c.id,u.workspace_id,u.name,u.role,c.kind,c.scope,COALESCE(c.application_id,''),c.permissions FROM oap_credentials c JOIN oap_users u ON u.id=c.user_id AND u.workspace_id=c.workspace_id WHERE "+where+" AND u.active AND c.revoked_at IS NULL AND c.expires_at>now()", arg).Scan(&p.UserID, &p.CredentialID, &p.WorkspaceID, &p.Name, &p.Role, &p.Kind, &p.Scope, &p.ApplicationID, &raw)
	if e != nil {
		return p, ErrDenied
	}
	if len(raw) > 0 && json.Unmarshal(raw, &p.Permissions) != nil {
		return domain.Principal{}, ErrDenied
	}
	if p.Kind == "agent" {
		control, err := s.Control(ctx)
		if err != nil || !control.Enabled {
			return domain.Principal{}, ErrDenied
		}
	}
	return p, nil
}
func (s Service) Rate(ctx context.Context, key string) bool {
	_, e := s.Pool.Exec(ctx, "DELETE FROM oap_auth_attempts WHERE until_at<now()")
	if e != nil {
		return false
	}
	var count int
	e = s.Pool.QueryRow(ctx, `INSERT INTO oap_auth_attempts(key,count,until_at) VALUES($1,1,now()+interval '1 minute') ON CONFLICT(key) DO UPDATE SET count=oap_auth_attempts.count+1 RETURNING count`, Hash(key)).Scan(&count)
	return e == nil && count <= 10
}
func (s Service) BeginAudit(ctx context.Context, p domain.Principal, action string) (string, error) {
	id := domain.NewID()
	_, e := s.Pool.Exec(ctx, "INSERT INTO oap_audit(id,user_id,credential_id,action) VALUES($1,$2,$3,$4)", id, p.UserID, p.CredentialID, action)
	return id, e
}
func (s Service) CompleteAudit(ctx context.Context, id string, status int) error {
	_, e := s.Pool.Exec(ctx, "UPDATE oap_audit SET status=$2,completed_at=now() WHERE id=$1", id, status)
	return e
}
