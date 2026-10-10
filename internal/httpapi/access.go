package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func (s *Server) accessService() access.Service { return access.Service{Pool: s.Controller.Store.Pool} }
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, e := url.Parse(origin)
	return e == nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" && u.Host == r.Host && (u.Scheme == "https" || (!s.SecureCookie && u.Scheme == "http"))
}
func (s *Server) cookieName() string {
	if s.SecureCookie {
		return "__Host-oap-session"
	}
	return "oap-session"
}
func (s *Server) session(w http.ResponseWriter, r *http.Request, user string) {
	secret, e := s.accessService().Issue(r.Context(), user, "session", "Browser session", "operate", "", 24*time.Hour)
	if e != nil {
		fail(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: secret, Path: "/", HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	p, e := s.accessService().Principal(r.Context(), secret)
	if e != nil {
		fail(w, e)
		return
	}
	write(w, 200, p)
}
func (s *Server) publicAccess(w http.ResponseWriter, r *http.Request) {
	if s.Preview {
		write(w, 200, map[string]any{"mode": "preview", "setupRequired": false})
		return
	}
	service := s.accessService()
	if r.Method == "GET" && r.URL.Path == "/api/v1/auth/status" {
		exists, e := service.HasOwner(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, map[string]any{"mode": "owner", "setupRequired": !exists})
		return
	}
	if r.Method != "POST" || r.Header.Get("X-OAP-CSRF") != "1" || !s.sameOrigin(r) {
		write(w, 403, map[string]string{"message": "Request origin is not allowed"})
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !service.Rate(r.Context(), "auth:"+host) {
		write(w, 429, map[string]string{"message": "Too many attempts. Try again in a minute."})
		return
	}
	var request struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Invite   string `json:"invite"`
	}
	if e := decode(w, r, &request); e != nil {
		write(w, 400, map[string]string{"message": "Invalid account request"})
		return
	}
	switch r.URL.Path {
	case "/api/v1/auth/setup":
		secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Setup ")
		if len(s.SetupToken) < 24 || !strings.HasPrefix(r.Header.Get("Authorization"), "Setup ") || subtle.ConstantTimeCompare([]byte(secret), []byte(s.SetupToken)) != 1 {
			write(w, 401, map[string]string{"message": "A valid setup secret is required"})
			return
		}
		if request.Invite != "" {
			write(w, 400, map[string]string{"message": "Use invite acceptance for invitations"})
			return
		}
		user, e := service.Register(r.Context(), request.Email, request.Name, request.Password, "")
		if e != nil {
			s.accountError(w, e)
			return
		}
		s.session(w, r, user)
	case "/api/v1/auth/invitations/accept":
		if len(request.Invite) < 32 {
			write(w, 401, map[string]string{"message": "Invitation is invalid or expired"})
			return
		}
		user, e := service.Register(r.Context(), request.Email, request.Name, request.Password, request.Invite)
		if e != nil {
			s.accountError(w, e)
			return
		}
		s.session(w, r, user)
	case "/api/v1/auth/login":
		audit, e := service.BeginAudit(r.Context(), domain.Principal{}, "auth.login")
		if e != nil {
			fail(w, e)
			return
		}
		user, e := service.Login(r.Context(), request.Email, request.Password)
		status := 200
		if e != nil {
			status = 401
		}
		if e = service.CompleteAudit(r.Context(), audit, status); e != nil {
			fail(w, e)
			return
		}
		if status == 401 {
			write(w, 401, map[string]string{"message": "Email or password is incorrect"})
			return
		}
		s.session(w, r, user)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) accountError(w http.ResponseWriter, e error) {
	if errors.Is(e, domain.ErrConflict) {
		fail(w, e)
	} else if errors.Is(e, access.ErrDenied) {
		write(w, 401, map[string]string{"message": "Invitation is invalid or expired"})
	} else {
		write(w, 422, map[string]string{"message": "Account could not be created. Check the email, name, and password length (12–256 bytes)."})
	}
}
func (s *Server) denied(w http.ResponseWriter, r *http.Request, p domain.Principal, status int, message string) {
	service := s.accessService()
	id, e := service.BeginAudit(r.Context(), p, r.Method+" "+r.URL.Path)
	if e == nil {
		_ = service.CompleteAudit(r.Context(), id, status)
	}
	write(w, status, map[string]string{"message": message})
}
func (s *Server) ownerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if workspace := r.Header.Get("X-OAP-Workspace"); workspace != "" && workspace != "default" {
			http.NotFound(w, r)
			return
		}
		secret := ""
		bearer := false
		if header := r.Header.Get("Authorization"); header != "" {
			if !strings.HasPrefix(header, "Bearer ") {
				write(w, 401, map[string]string{"message": "Sign in to continue"})
				return
			}
			secret = strings.TrimPrefix(header, "Bearer ")
			bearer = true
		} else if cookie, e := r.Cookie(s.cookieName()); e == nil {
			secret = cookie.Value
		}
		p, e := s.accessService().Principal(r.Context(), secret)
		if e != nil || (bearer && p.Kind != "agent") || (!bearer && p.Kind != "session") {
			write(w, 401, map[string]string{"message": "Sign in to continue"})
			return
		}
		if !s.sameOrigin(r) || (r.Method != "GET" && r.Method != "HEAD" && !bearer && r.Header.Get("X-OAP-CSRF") != "1") {
			write(w, 403, map[string]string{"message": "Request origin is not allowed"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/access/") && (p.Role != "owner" || p.Kind != "session") {
			s.denied(w, r, p, 403, "Owner access is required")
			return
		}
		if p.ApplicationID != "" && !s.applicationScope(r, p) {
			s.denied(w, r, p, 404, "Resource not found")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/api/v1/auth/logout" && !p.Operates(p.ApplicationID) {
			s.denied(w, r, p, 403, "This credential is read-only")
			return
		}
		ctx := domain.WithPrincipal(r.Context(), p)
		if r.Method == "GET" || r.Method == "HEAD" {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		audit, e := s.accessService().BeginAudit(ctx, p, r.Method+" "+r.URL.Path)
		if e != nil {
			write(w, 503, map[string]string{"message": "Audit storage is unavailable; no action was started"})
			return
		}
		recorder := &auditResponse{ResponseWriter: w, status: 200}
		next.ServeHTTP(recorder, r.WithContext(ctx))
		finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.accessService().CompleteAudit(finish, audit, recorder.status)
	})
}

type auditResponse struct {
	http.ResponseWriter
	status int
}

func (w *auditResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (s *Server) applicationScope(r *http.Request, p domain.Principal) bool {
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.Path == "/api/v1/meta" || r.URL.Path == "/api/v1/auth/me" {
		return r.Method == "GET"
	}
	if len(path) >= 4 && path[2] == "applications" {
		return path[3] == p.ApplicationID
	}
	if len(path) >= 4 && path[2] == "deployments" {
		d, e := s.Controller.Store.Deployment(r.Context(), path[3])
		return e == nil && d.ApplicationID == p.ApplicationID
	}
	return false
}
func (s *Server) accessRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) { p, _ := domain.Identity(r.Context()); write(w, 200, p) })
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		p, _ := domain.Identity(r.Context())
		_, e := s.Controller.Store.Pool.Exec(r.Context(), "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1", p.CredentialID)
		if e != nil {
			fail(w, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode})
		write(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/access/members", func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.Controller.Store.Pool.Query(r.Context(), "SELECT id,email,name,role,active FROM oap_users ORDER BY created_at")
		if e != nil {
			fail(w, e)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, email, name, role string
			var active bool
			if e = rows.Scan(&id, &email, &name, &role, &active); e != nil {
				fail(w, e)
				return
			}
			out = append(out, map[string]any{"id": id, "email": email, "name": name, "role": role, "active": active})
		}
		if e = rows.Err(); e != nil {
			fail(w, e)
			return
		}
		write(w, 200, out)
	})
	mux.HandleFunc("PATCH /api/v1/access/members/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Role   string `json:"role"`
			Active bool   `json:"active"`
		}
		if decode(w, r, &v) != nil || (v.Role != "operator" && v.Role != "viewer") {
			write(w, 422, map[string]string{"message": "Choose operator or viewer"})
			return
		}
		tx, e := s.Controller.Store.Pool.Begin(r.Context())
		if e != nil {
			fail(w, e)
			return
		}
		defer tx.Rollback(r.Context())
		result, e := tx.Exec(r.Context(), "UPDATE oap_users SET role=$2,active=$3 WHERE id=$1 AND role!='owner'", r.PathValue("id"), v.Role, v.Active)
		if e != nil {
			fail(w, e)
			return
		}
		if result.RowsAffected() != 1 {
			http.NotFound(w, r)
			return
		}
		if !v.Active {
			if _, e = tx.Exec(r.Context(), "UPDATE oap_credentials SET revoked_at=now() WHERE user_id=$1", r.PathValue("id")); e != nil {
				fail(w, e)
				return
			}
		}
		if e = tx.Commit(r.Context()); e != nil {
			fail(w, e)
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/v1/access/invitations", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		e := decode(w, r, &v)
		email, validation := access.Email(v.Email)
		if e != nil || validation != nil || (v.Role != "operator" && v.Role != "viewer") {
			write(w, 422, map[string]string{"message": "Provide an email and operator or viewer role"})
			return
		}
		secret := access.Secret()
		_, e = s.Controller.Store.Pool.Exec(r.Context(), "INSERT INTO oap_invitations(id,email,role,token_hash,expires_at) VALUES($1,$2,$3,$4,now()+interval '48 hours')", domain.NewID(), email, v.Role, access.Hash(secret))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 201, map[string]string{"invite": secret, "expiresIn": "48 hours"})
	})
	mux.HandleFunc("GET /api/v1/access/credentials", func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.Controller.Store.Pool.Query(r.Context(), "SELECT id,name,scope,application_id,expires_at,revoked_at IS NOT NULL FROM oap_credentials WHERE kind='agent' ORDER BY created_at DESC LIMIT 100")
		if e != nil {
			fail(w, e)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name, scope, app string
			var expires time.Time
			var revoked bool
			if e = rows.Scan(&id, &name, &scope, &app, &expires, &revoked); e != nil {
				fail(w, e)
				return
			}
			out = append(out, map[string]any{"id": id, "name": name, "scope": scope, "applicationId": app, "expiresAt": expires, "revoked": revoked})
		}
		if e = rows.Err(); e != nil {
			fail(w, e)
			return
		}
		write(w, 200, out)
	})
	mux.HandleFunc("POST /api/v1/access/credentials", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name          string `json:"name"`
			Scope         string `json:"scope"`
			ApplicationID string `json:"applicationId"`
		}
		if decode(w, r, &v) != nil || v.ApplicationID == "" {
			write(w, 422, map[string]string{"message": "Choose an application and credential scope"})
			return
		}
		if _, e := s.Controller.Store.Application(r.Context(), v.ApplicationID); e != nil {
			fail(w, e)
			return
		}
		p, _ := domain.Identity(r.Context())
		secret, e := s.accessService().Issue(r.Context(), p.UserID, "agent", v.Name, v.Scope, v.ApplicationID, 30*24*time.Hour)
		if e != nil {
			write(w, 422, map[string]string{"message": "Choose read or operate scope and a name (1–80 bytes)"})
			return
		}
		write(w, 201, map[string]string{"token": secret})
	})
	mux.HandleFunc("DELETE /api/v1/access/credentials/{id}", func(w http.ResponseWriter, r *http.Request) {
		result, e := s.Controller.Store.Pool.Exec(r.Context(), "UPDATE oap_credentials SET revoked_at=now() WHERE id=$1 AND kind='agent'", r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		if result.RowsAffected() != 1 {
			http.NotFound(w, r)
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/access/audit", func(w http.ResponseWriter, r *http.Request) {
		rows, e := s.Controller.Store.Pool.Query(r.Context(), "SELECT a.id,COALESCE(u.name,'Anonymous'),COALESCE(a.credential_id,''),a.action,COALESCE(a.status,0),a.created_at,COALESCE(c.name,''),COALESCE(c.kind,'') FROM oap_audit a LEFT JOIN oap_users u ON u.id=a.user_id LEFT JOIN oap_credentials c ON c.id=a.credential_id ORDER BY a.created_at DESC LIMIT 100")
		if e != nil {
			fail(w, e)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, name, credential, action, credentialName, kind string
			var status int
			var at time.Time
			if e = rows.Scan(&id, &name, &credential, &action, &status, &at, &credentialName, &kind); e != nil {
				fail(w, e)
				return
			}
			out = append(out, map[string]any{"id": id, "subject": name, "credentialId": credential, "credentialName": credentialName, "kind": kind, "action": action, "status": status, "createdAt": at})
		}
		if e = rows.Err(); e != nil {
			fail(w, e)
			return
		}
		write(w, 200, out)
	})
}
