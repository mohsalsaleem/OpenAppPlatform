package controller

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func (c *Controller) authorizeRelease(ctx context.Context, d domain.Deployment) error {
	if !c.RequireIdentity {
		return nil
	}
	var credential string
	if e := c.Store.Pool.QueryRow(ctx, "SELECT COALESCE(credential_id,'') FROM oap_deployments WHERE id=$1 AND workspace_id='default'", d.ID).Scan(&credential); e != nil {
		return e
	}
	p, e := (access.Service{Pool: c.Store.Pool}).ByID(ctx, credential)
	if e != nil || !p.Operates(d.ApplicationID) {
		return errors.New("release credential is missing, expired, revoked, or no longer authorized; owner recovery is required")
	}
	return nil
}
func (c *Controller) recordCredential(ctx context.Context, tx pgx.Tx, id, app string) error {
	p, ok := domain.Identity(ctx)
	if !ok {
		if c.RequireIdentity {
			return access.ErrDenied
		}
		return nil
	}
	var role, scope, allowedApp string
	e := tx.QueryRow(ctx, "SELECT u.role,c.scope,COALESCE(c.application_id,'') FROM oap_credentials c JOIN oap_users u ON u.id=c.user_id AND u.workspace_id=c.workspace_id WHERE c.id=$1 AND u.active AND c.revoked_at IS NULL AND c.expires_at>now() AND c.workspace_id='default'", p.CredentialID).Scan(&role, &scope, &allowedApp)
	if e != nil || (role != "owner" && role != "operator") || scope != "operate" || (allowedApp != "" && allowedApp != app) {
		return access.ErrDenied
	}
	_, e = tx.Exec(ctx, "UPDATE oap_deployments SET credential_id=$2 WHERE id=$1", id, p.CredentialID)
	if e != nil {
		return e
	}
	return linkAuditOperation(ctx, tx, id)
}

// Commit the correlation with the operation: a crash after commit cannot orphan it.
func linkAuditOperation(ctx context.Context, tx pgx.Tx, id string) error {
	audit := domain.AuditIntent(ctx)
	if audit == "" {
		return nil
	} // Internal/preview callers have no HTTP intent.
	result, err := tx.Exec(ctx, "UPDATE oap_audit SET deployment_id=$2 WHERE id=$1 AND workspace_id='default' AND (deployment_id IS NULL OR deployment_id=$2)", audit, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("durable audit intent is missing or conflicts with the operation")
	}
	return nil
}
