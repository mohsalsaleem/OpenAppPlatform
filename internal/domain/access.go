package domain

import (
	"context"
	"slices"
)

type Principal struct {
	UserID        string            `json:"userId"`
	CredentialID  string            `json:"credentialId"`
	WorkspaceID   string            `json:"workspaceId"`
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	Kind          string            `json:"kind"`
	Scope         string            `json:"scope"`
	ApplicationID string            `json:"applicationId,omitempty"`
	Permissions   *AgentPermissions `json:"permissions,omitempty"`
}
type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func Identity(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
func (p Principal) Operates(app string) bool {
	return p.WorkspaceID == "default" && (p.Role == "owner" || p.Role == "operator") && p.Scope == "operate" && (p.ApplicationID == "" || p.ApplicationID == app)
}

// AuditIntent carries only the durable intent ID, never the request payload.
type auditIntentKey struct{}

func WithAuditIntent(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, auditIntentKey{}, id)
}
func AuditIntent(ctx context.Context) string {
	id, _ := ctx.Value(auditIntentKey{}).(string)
	return id
}

// AgentPermissions pins a credential to one logical target/environment and an allowlist.
type AgentPermissions struct {
	Actions     []string `json:"actions"`
	TargetID    string   `json:"targetId"`
	Environment string   `json:"environment"`
}

var AgentActions = []string{"deploy", "restart", "scale-down", "rollback", "recover", "control", "configure"}

func (p Principal) Reads(app, target, environment string) bool {
	return p.WorkspaceID == "default" && (p.ApplicationID == "" || p.ApplicationID == app) &&
		(p.Permissions == nil || (p.Permissions.TargetID == target && p.Permissions.Environment == environment))
}
func (p Principal) Allows(app, action, target, environment string) bool {
	return p.Operates(app) && p.Reads(app, target, environment) &&
		(p.Permissions == nil || (slices.Contains(AgentActions, action) && slices.Contains(p.Permissions.Actions, action)))
}

type actionKey struct{}

func WithAction(ctx context.Context, action string) context.Context {
	return context.WithValue(ctx, actionKey{}, action)
}
func Action(ctx context.Context) string { action, _ := ctx.Value(actionKey{}).(string); return action }
