package controller

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/routing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
	"github.com/riverqueue/river"
)

type RouteAutomation struct {
	Enabled   bool      `json:"enabled"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checkedAt"`
}
type RouteReconcileArgs struct{}

func (RouteReconcileArgs) Kind() string { return "route_reconcile" }

type RouteReconcileWorker struct {
	river.WorkerDefaults[RouteReconcileArgs]
	Controller *Controller
}

func (w *RouteReconcileWorker) Timeout(*river.Job[RouteReconcileArgs]) time.Duration {
	return 2 * time.Minute
}
func (w *RouteReconcileWorker) Work(ctx context.Context, _ *river.Job[RouteReconcileArgs]) error {
	return w.Controller.ReconcileRoutes(ctx)
}

func automationView(row db.OapRouteAutomation) *RouteAutomation {
	return &RouteAutomation{Enabled: row.Enabled, Status: row.Status, Message: row.Message, CheckedAt: row.CheckedAt.Time}
}
func (c *Controller) automationAuthority(ctx context.Context, app string, b routing.Binding) (string, error) {
	a, e := c.Store.Application(ctx, app)
	if e != nil {
		return "", e
	}
	target, e := c.Store.Target(ctx, a.Manifest.TargetID)
	if e != nil {
		return "", e
	}
	return routeHash(struct {
		Binding    routing.Binding
		Target     domain.Target
		Credential string
	}{b, target, target.TokenEnv}), nil
}
func (c *Controller) SetRouteAutomation(ctx context.Context, app, name string, version int64, enabled bool) (*RouteAutomation, error) {
	owner, e := routeOwner(ctx)
	if e != nil {
		return nil, e
	}
	b, e := c.routeBinding(app, name)
	if e != nil {
		return nil, e
	}
	conn, e := c.routeLock(ctx, app)
	if e != nil {
		return nil, e
	}
	defer closeRouteLock(conn)
	q := db.New(conn)
	a, e := c.Store.Application(ctx, app)
	if e != nil {
		return nil, e
	}
	if a.Version != version {
		return nil, domain.ErrConflict
	}
	row, e := q.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app, Name: name})
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if !enabled {
		if errors.Is(e, pgx.ErrNoRows) {
			return &RouteAutomation{Status: "disabled", CheckedAt: time.Now().UTC()}, nil
		}
		if row.PendingID != "" {
			p, err := q.GetRoutePublication(ctx, db.GetRoutePublicationParams{ApplicationID: app, ID: row.PendingID})
			if err != nil {
				return nil, err
			}
			if p.State == "prepared" {
				if err = q.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app, ID: row.PendingID, State: "cancelled"}); err != nil {
					return nil, err
				}
				row.PendingID = ""
			}
		}
		e = q.UpdateRouteAutomation(ctx, db.UpdateRouteAutomationParams{ApplicationID: app, Name: name, Enabled: false, NativeHash: row.NativeHash, PendingID: row.PendingID, Status: "disabled", Message: "Automatic updates are disabled. Any uncertain publication still needs owner recovery."})
	} else {
		active, err := q.RouteAutomationOwnerActive(ctx, owner.UserID)
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, domain.ErrConflict
		}
		uncertain, err := q.HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: app})
		if err != nil {
			return nil, err
		}
		if uncertain {
			return nil, domain.ErrConflict
		}
		snapshot, allowed, err := c.routeSnapshot(ctx, app, b)
		if err != nil {
			return nil, err
		}
		f, err := routing.Open(b)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw, err := f.Read()
		if err != nil {
			return nil, err
		}
		urls, err := routing.Backends(b, raw, allowed)
		if err != nil {
			return nil, err
		}
		if len(difference(urls, desiredURLs(snapshot)))+len(difference(desiredURLs(snapshot), urls)) > 0 {
			return nil, errors.New("apply current membership before enabling automatic updates")
		}
		directory, err := f.Identity()
		if err != nil {
			return nil, err
		}
		authority, err := c.automationAuthority(ctx, app, b)
		if err != nil {
			return nil, err
		}
		if row.PendingID != "" {
			p, err := q.GetRoutePublication(ctx, db.GetRoutePublicationParams{ApplicationID: app, ID: row.PendingID})
			if err != nil {
				return nil, err
			}
			if p.State == "prepared" {
				if err = q.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app, ID: row.PendingID, State: "cancelled"}); err != nil {
					return nil, err
				}
			}
		}
		e = q.SaveRouteAutomation(ctx, db.SaveRouteAutomationParams{ApplicationID: app, Name: name, OwnerID: owner.UserID, AuthorityHash: authority, DirectoryHash: directory, NativeHash: routing.Hash(raw)})
	}
	if e != nil {
		return nil, e
	}
	row, e = q.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app, Name: name})
	if e != nil {
		return nil, e
	}
	return automationView(row), nil
}

// Only trusted periodic work calls this method. Human grants are checked anew
// under the same admission lock as publication and lifecycle changes.
func (c *Controller) ReconcileRoutes(ctx context.Context) error {
	rows, e := db.New(c.Store.Pool).ListRouteAutomation(ctx)
	if e != nil {
		return e
	}
	for _, row := range rows {
		if e = c.reconcileRoute(ctx, row.ApplicationID, row.Name); e != nil && !errors.Is(e, domain.ErrConflict) {
			return e
		}
	}
	return nil
}
func (c *Controller) reconcileRoute(ctx context.Context, app, name string) error {
	conn, e := c.routeLock(ctx, app)
	if e != nil {
		return e
	}
	defer closeRouteLock(conn)
	q := db.New(conn)
	row, e := q.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: app, Name: name})
	if e != nil {
		return e
	}
	if !row.Enabled {
		return nil
	}
	record := func(enabled bool, status, message string) error {
		return q.UpdateRouteAutomation(ctx, db.UpdateRouteAutomationParams{ApplicationID: app, Name: name, Enabled: enabled, NativeHash: row.NativeHash, PendingID: row.PendingID, Status: status, Message: message})
	}
	active, e := q.RouteAutomationOwnerActive(ctx, row.OwnerID)
	if e != nil {
		return e
	}
	if !active {
		return record(false, "paused", "The owner grant is no longer active.")
	}
	b, e := c.routeBinding(app, name)
	if e != nil {
		return record(false, "paused", "Configured route authority is unavailable.")
	}
	authority, e := c.automationAuthority(ctx, app, b)
	if e != nil {
		return e
	}
	if authority != row.AuthorityHash {
		return record(false, "paused", "Configured route or target authority changed. Review and enable again.")
	}
	uncertain, e := q.HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: app, ID: row.PendingID})
	if e != nil {
		return e
	}
	if uncertain {
		return record(false, "paused", "Another publication needs owner recovery.")
	}
	f, e := routing.Open(b)
	if e != nil {
		return record(false, "paused", "Native route file is unavailable. Inspect routing authority.")
	}
	defer f.Close()
	directory, e := f.Identity()
	if e != nil {
		return e
	}
	if directory != row.DirectoryHash {
		return record(false, "paused", "Native route directory changed. Inspect routing authority.")
	}
	raw, e := f.Read()
	if e != nil {
		return record(false, "paused", "Native route cannot be read safely.")
	}
	var plan RoutePlan
	state := ""
	if row.PendingID != "" {
		saved, err := q.GetRoutePublication(ctx, db.GetRoutePublicationParams{ApplicationID: app, ID: row.PendingID})
		if err != nil {
			return err
		}
		if err = json.Unmarshal(saved.Plan, &plan); err != nil {
			return err
		}
		state = saved.State
		if saved.OwnerID != row.OwnerID || !plan.Automatic || plan.Name != name {
			return record(false, "paused", "Publication authority needs owner inspection.")
		}
		if state != "prepared" && state != "publishing" {
			return record(false, "paused", "Publication changed outside automatic management. Review and enable again.")
		}
	}
	hash := routing.Hash(raw)
	if hash != row.NativeHash && !(state == "publishing" && hash == plan.DesiredHash) {
		return record(false, "paused", "Native route drift detected. No automatic changes were made.")
	}
	fresh, allowed, e := c.routeSnapshot(ctx, app, b)
	if e != nil {
		if state == "publishing" {
			return record(false, "paused", "Uncertain publication needs owner recovery; current backend state cannot be verified.")
		}
		return record(true, "blocked", "Waiting for idle application work and a complete owned binding set with at least one healthy backend.")
	}
	if _, e = routing.Backends(b, raw, allowed); e != nil {
		return record(false, "paused", "Native route policy or ownership changed.")
	}
	if row.PendingID != "" && (fresh.AuthorityHash != plan.AuthorityHash || fresh.DesiredHash != plan.DesiredHash || fresh.DesiredConfig != plan.DesiredConfig) {
		if state == "publishing" {
			return record(false, "paused", "Backend state changed during an uncertain publication. Owner recovery is required.")
		}
		if e = q.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app, ID: row.PendingID, State: "cancelled"}); e != nil {
			return e
		}
		row.PendingID = ""
		return record(true, "blocked", "Backend state changed before dispatch; the next check will create a fresh intent.")
	}
	if row.PendingID == "" {
		urls, e := routing.Backends(b, raw, allowed)
		if e != nil {
			return e
		}
		if len(difference(urls, desiredURLs(fresh)))+len(difference(desiredURLs(fresh), urls)) == 0 {
			return record(true, "in-sync", "")
		}
		plan = fresh
		plan.ID = domain.NewID()
		plan.Automatic = true
		plan.BeforeHash = hash
		plan.DirectoryHash = directory
		plan.CreatedAt = time.Now().UTC()
		plan.State = "prepared"
		plan.Added = difference(desiredURLs(plan), urls)
		plan.Removed = difference(urls, desiredURLs(plan))
		payload, e := json.Marshal(plan)
		if e != nil {
			return e
		}
		tx, e := conn.Begin(ctx)
		if e != nil {
			return e
		}
		defer rollbackRouteTransaction(tx)
		tq := db.New(tx)
		if e = tq.InsertRoutePublication(ctx, db.InsertRoutePublicationParams{ID: plan.ID, ApplicationID: app, OwnerID: row.OwnerID, Plan: payload}); e != nil {
			return e
		}
		row.PendingID = plan.ID
		if e = tq.UpdateRouteAutomation(ctx, db.UpdateRouteAutomationParams{ApplicationID: app, Name: name, Enabled: true, NativeHash: row.NativeHash, PendingID: plan.ID, Status: "updating"}); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		// Re-observe immediately before arming durable intent, including native health.
		latest, _, err := c.routeSnapshot(ctx, app, b)
		if err != nil || latest.AuthorityHash != plan.AuthorityHash {
			return record(true, "blocked", "Backend state changed before dispatch; awaiting a fresh check.")
		}
	}
	if e = q.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app, ID: plan.ID, State: "publishing"}); e != nil {
		return e
	}
	// Reopen the configured path before dispatch to detect a replaced mount.
	reopened, e := routing.Open(b)
	if e != nil {
		return record(false, "paused", "Native route needs owner recovery.")
	}
	identity, e := reopened.Identity()
	reopened.Close()
	if e != nil || identity != row.DirectoryHash {
		return record(false, "paused", "Native directory changed during publication. Owner recovery is required.")
	}
	if _, e = f.Publish(plan.BeforeHash, []byte(plan.DesiredConfig)); e != nil {
		return record(false, "paused", "Publication outcome is uncertain. Resume or cancel through owner recovery.")
	}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return e
	}
	defer rollbackRouteTransaction(tx)
	tq := db.New(tx)
	if e = tq.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: app, ID: plan.ID, State: "applied"}); e != nil {
		return e
	}
	if e = tq.UpdateRouteAutomation(ctx, db.UpdateRouteAutomationParams{ApplicationID: app, Name: name, Enabled: true, NativeHash: plan.DesiredHash, PendingID: "", Status: "in-sync"}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func rollbackRouteTransaction(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
