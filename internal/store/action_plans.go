package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type ActionResult struct {
	ApplicationID     string `json:"applicationId"`
	DefinitionVersion int64  `json:"definitionVersion"`
	OperationID       string `json:"operationId,omitempty"`
	State             string `json:"state"`
}
type ActionPlan struct {
	ID               string          `json:"id"`
	Action           string          `json:"action"`
	CredentialID     string          `json:"credentialId"`
	ApplicationID    string          `json:"applicationId,omitempty"`
	TargetID         string          `json:"targetId"`
	Environment      string          `json:"environment"`
	ExpectedVersion  int64           `json:"expectedVersion"`
	Hash             string          `json:"planHash"`
	RequiresReview   bool            `json:"requiresReview"`
	ApprovedAt       *time.Time      `json:"approvedAt,omitempty"`
	Result           *ActionResult   `json:"result,omitempty"`
	ExpiresAt        time.Time       `json:"expiresAt"`
	CreatedAt        time.Time       `json:"createdAt"`
	Summary          json.RawMessage `json:"summary"`
	DefinitionHash   string          `json:"-"`
	TargetHash       string          `json:"-"`
	InputFingerprint string          `json:"-"`
	Payload          json.RawMessage `json:"-"`
	ApprovedBy       string          `json:"-"`
}

func HashJSON(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func scanActionPlan(row pgx.Row) (ActionPlan, error) {
	var p ActionPlan
	var result []byte
	err := row.Scan(&p.ID, &p.Action, &p.CredentialID, &p.ApplicationID, &p.TargetID, &p.Environment, &p.ExpectedVersion, &p.DefinitionHash, &p.TargetHash, &p.InputFingerprint, &p.Payload, &p.Summary, &p.Hash, &p.RequiresReview, &p.ApprovedBy, &p.ApprovedAt, &result, &p.ExpiresAt, &p.CreatedAt)
	if len(result) > 0 {
		var r ActionResult
		if json.Unmarshal(result, &r) != nil {
			return p, errors.New("invalid stored plan outcome")
		}
		p.Result = &r
	}
	return p, mapError(err)
}

const actionPlanSelect = `SELECT id,action,credential_id,COALESCE(application_id,''),target_id,environment,definition_version,definition_hash,target_hash,input_fingerprint,payload,public_summary,plan_hash,requires_review,COALESCE(approved_by,''),approved_at,result,expires_at,created_at FROM oap_action_plans WHERE id=$1`

func (s *Store) ActionPlan(ctx context.Context, id string) (ActionPlan, error) {
	return scanActionPlan(s.Pool.QueryRow(ctx, actionPlanSelect, id))
}
func (s *Store) SaveActionPlan(ctx context.Context, p ActionPlan) (ActionPlan, error) {
	err := s.Pool.QueryRow(ctx, `INSERT INTO oap_action_plans(id,action,credential_id,application_id,target_id,environment,definition_version,definition_hash,target_hash,input_fingerprint,payload,public_summary,plan_hash,requires_review,expires_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING created_at`, p.ID, p.Action, p.CredentialID, p.ApplicationID, p.TargetID, p.Environment, p.ExpectedVersion, p.DefinitionHash, p.TargetHash, p.InputFingerprint, []byte(p.Payload), []byte(p.Summary), p.Hash, p.RequiresReview, p.ExpiresAt).Scan(&p.CreatedAt)
	return p, err
}

type activeActionPlan struct{ ID, Action, Inputs string }
type activeActionKey struct{}

func WithActionPlan(ctx context.Context, id, action, inputs string) context.Context {
	return context.WithValue(ctx, activeActionKey{}, activeActionPlan{id, action, inputs})
}
func ActiveActionPlan(ctx context.Context) string {
	v, _ := ctx.Value(activeActionKey{}).(activeActionPlan)
	return v.ID
}

// BeginActionPlan is called inside the same transaction as the existing mutation.
// Replays return the committed receipt, never a second effect. App locks precede
// this row lock on app actions; create uses only the plan/credential lock.
func (s *Store) BeginActionPlan(ctx context.Context, tx pgx.Tx) (*ActionResult, error) {
	active, _ := ctx.Value(activeActionKey{}).(activeActionPlan)
	if active.ID == "" {
		return nil, nil
	}
	p, err := scanActionPlan(tx.QueryRow(ctx, actionPlanSelect+" FOR UPDATE", active.ID))
	if err != nil {
		return nil, err
	}
	identity, ok := domain.Identity(ctx)
	if !ok || identity.CredentialID != p.CredentialID || active.Action != p.Action {
		return nil, domain.ErrConflict
	}
	var current domain.Principal
	var raw []byte
	credentialLock := " FOR SHARE OF c,u"
	if p.Action == "create" {
		credentialLock = " FOR UPDATE OF c FOR SHARE OF u"
	}
	err = tx.QueryRow(ctx, `SELECT c.id,c.kind,c.scope,COALESCE(c.application_id,''),c.permissions,u.workspace_id,u.role FROM oap_credentials c JOIN oap_users u ON u.id=c.user_id WHERE c.id=$1 AND c.revoked_at IS NULL AND c.expires_at>now() AND u.active`+credentialLock, identity.CredentialID).Scan(&current.CredentialID, &current.Kind, &current.Scope, &current.ApplicationID, &raw, &current.WorkspaceID, &current.Role)
	if err != nil {
		return nil, domain.ErrConflict
	}
	if len(raw) > 0 && json.Unmarshal(raw, &current.Permissions) != nil {
		return nil, domain.ErrConflict
	}
	if current.Kind == "agent" {
		var enabled bool
		if tx.QueryRow(ctx, "SELECT enabled FROM oap_agent_control WHERE workspace_id='default' FOR SHARE").Scan(&enabled) != nil || !enabled {
			return nil, domain.ErrConflict
		}
	}
	if current.TargetGrant() && p.ApplicationID != "" {
		var owned bool
		if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_agent_applications WHERE credential_id=$1 AND application_id=$2)", current.CredentialID, p.ApplicationID).Scan(&owned) != nil || !owned {
			return nil, domain.ErrConflict
		}
		current.ManagedApplicationIDs = []string{p.ApplicationID}
	}
	allowed := current.Allows(p.ApplicationID, p.Action, p.TargetID, p.Environment)
	if p.Action == "create" {
		allowed = current.Creates(p.TargetID, p.Environment) || (current.Kind == "session" && current.Operates(""))
	}
	if !allowed {
		return nil, domain.ErrConflict
	}
	if p.Result != nil {
		return p.Result, nil
	}
	if !p.ExpiresAt.After(time.Now()) || active.Inputs != p.InputFingerprint {
		return nil, domain.ErrConflict
	}
	if p.RequiresReview {
		var approved bool
		if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oap_credentials c JOIN oap_users u ON u.id=c.user_id WHERE c.id=$1 AND c.kind='session' AND c.revoked_at IS NULL AND c.expires_at>now() AND u.active AND u.role='owner')`, p.ApprovedBy).Scan(&approved) != nil || p.ApprovedAt == nil || !approved {
			return nil, domain.ErrConflict
		}
	}
	target, err := s.TargetTx(ctx, tx, p.TargetID)
	if err != nil || HashJSON(target) != p.TargetHash {
		return nil, domain.ErrConflict
	}
	if p.ApplicationID != "" {
		app, err := s.ApplicationTx(ctx, tx, p.ApplicationID)
		if err != nil || app.Version != p.ExpectedVersion || HashJSON(app.Manifest) != p.DefinitionHash {
			return nil, domain.ErrConflict
		}
	}
	return nil, nil
}
func (s *Store) FinishActionPlan(ctx context.Context, tx pgx.Tx, result ActionResult) error {
	id := ActiveActionPlan(ctx)
	if id == "" {
		return nil
	}
	raw, _ := json.Marshal(result)
	affected, err := tx.Exec(ctx, "UPDATE oap_action_plans SET result=$2 WHERE id=$1 AND result IS NULL", id, raw)
	if err != nil {
		return err
	}
	if affected.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
