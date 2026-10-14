package domain

import (
	"context"
	"slices"
)

type Principal struct {
	UserID                string            `json:"userId"`
	CredentialID          string            `json:"credentialId"`
	WorkspaceID           string            `json:"workspaceId"`
	Name                  string            `json:"name"`
	Role                  string            `json:"role"`
	Kind                  string            `json:"kind"`
	Scope                 string            `json:"scope"`
	ApplicationID         string            `json:"applicationId,omitempty"`
	ManagedApplicationIDs []string          `json:"managedApplicationIds,omitempty"`
	Permissions           *AgentPermissions `json:"permissions,omitempty"`
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
	return p.WorkspaceID == "default" && (p.Role == "owner" || p.Role == "operator") && p.Scope == "operate" && (p.ApplicationID == "" || p.ApplicationID == app) && p.managedScope(app)
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
	Actions            []string `json:"actions"`
	TargetID           string   `json:"targetId"`
	Environment        string   `json:"environment"`
	CreateApplications bool     `json:"createApplications,omitempty"`
	MaxApplications    int      `json:"maxApplications,omitempty"`
}

var AgentActions = []string{"deploy", "restart", "scale-down", "remove-component", "rollback", "recover", "control", "configure"}

func (p Principal) Reads(app, target, environment string) bool {
	return p.WorkspaceID == "default" && (p.ApplicationID == "" || p.ApplicationID == app) && p.managedScope(app) &&
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

func (p Principal) TargetGrant() bool {
	return p.Kind == "agent" && p.Permissions != nil && p.Permissions.CreateApplications
}
func (p Principal) managedScope(app string) bool {
	return !p.TargetGrant() || slices.Contains(p.ManagedApplicationIDs, app)
}
func (p Principal) Creates(target, environment string) bool {
	return p.TargetGrant() && p.Scope == "operate" && p.WorkspaceID == "default" && (p.Role == "owner" || p.Role == "operator") && p.ApplicationID == "" && p.Permissions.TargetID == target && p.Permissions.Environment == environment
}

type creationKey struct{}

func WithCreationKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, creationKey{}, key)
}
func CreationKey(ctx context.Context) string { key, _ := ctx.Value(creationKey{}).(string); return key }
