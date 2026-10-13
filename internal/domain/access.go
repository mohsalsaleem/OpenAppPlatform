package domain

import "context"

type Principal struct {
	UserID        string `json:"userId"`
	CredentialID  string `json:"credentialId"`
	WorkspaceID   string `json:"workspaceId"`
	Name          string `json:"name"`
	Role          string `json:"role"`
	Kind          string `json:"kind"`
	Scope         string `json:"scope"`
	ApplicationID string `json:"applicationId,omitempty"`
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
