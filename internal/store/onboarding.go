package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
)

func authorizeCreation(ctx context.Context, tx pgx.Tx, m domain.Manifest, raw []byte) (string, string, *domain.Application, error) {
	p, ok := domain.Identity(ctx)
	if !ok || !p.TargetGrant() {
		return "", "", nil, nil
	}
	key := domain.CreationKey(ctx)
	if len(key) < 1 || len(key) > 128 || strings.ContainsAny(key, "\r\n\x00") {
		return "", "", nil, errors.New("agent creation requires a stable idempotency key of 1–128 bytes")
	}
	var permissions domain.AgentPermissions
	var encoded []byte
	var enabled, valid bool
	// Serialize creation count and freeze credential validity against revocation.
	if err := tx.QueryRow(ctx, "SELECT c.permissions FROM oap_credentials c JOIN oap_users u ON u.id=c.user_id WHERE c.id=$1 AND c.target_grant AND c.revoked_at IS NULL AND c.expires_at>now() AND u.active AND u.role IN ('owner','operator') FOR UPDATE OF c", p.CredentialID).Scan(&encoded); err != nil {
		return "", "", nil, domain.ErrConflict
	}
	if json.Unmarshal(encoded, &permissions) != nil {
		return "", "", nil, domain.ErrConflict
	}
	if err := tx.QueryRow(ctx, "SELECT enabled FROM oap_agent_control WHERE workspace_id='default' FOR SHARE").Scan(&enabled); err != nil || !enabled {
		return "", "", nil, domain.ErrConflict
	}
	if !permissions.CreateApplications || permissions.TargetID != m.TargetID || permissions.Environment != m.Environment {
		return "", "", nil, domain.ErrConflict
	}
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_targets WHERE id=$1 AND spec->>'operator'='docker' AND spec->>'environment'=$2)", m.TargetID, m.Environment).Scan(&valid); err != nil || !valid {
		return "", "", nil, domain.ErrConflict
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var existing, oldHash string
	err := tx.QueryRow(ctx, "SELECT application_id,request_hash FROM oap_agent_applications WHERE credential_id=$1 AND creation_key=$2", p.CredentialID, key).Scan(&existing, &oldHash)
	if err == nil {
		if oldHash != hash {
			return "", "", nil, domain.ErrConflict
		}
		app, e := scanApp(tx.QueryRow(ctx, "SELECT id,spec,created_at,updated_at,version,group_id FROM oap_applications WHERE id=$1", existing))
		return p.CredentialID, key, &app, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM oap_agent_applications WHERE credential_id=$1", p.CredentialID).Scan(&count); err != nil {
		return "", "", nil, err
	}
	if count >= permissions.MaxApplications {
		return "", "", nil, domain.ErrConflict
	}
	return p.CredentialID, key, nil, nil
}
