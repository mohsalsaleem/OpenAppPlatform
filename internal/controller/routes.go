package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/routing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
)

type RouteMember struct {
	Ordinal    int    `json:"ordinal"`
	ResourceID string `json:"resourceId"`
	URL        string `json:"url"`
	Retired    bool   `json:"retired"`
	Excluded   bool   `json:"excluded"`
	Status     string `json:"status"`
}
type RoutePlan struct {
	Automatic       bool          `json:"automatic,omitempty"`
	DirectoryHash   string        `json:"directoryHash"`
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Component       string        `json:"component"`
	URL             string        `json:"url"`
	ExpectedVersion int64         `json:"expectedVersion"`
	AuthorityHash   string        `json:"authorityHash"`
	Members         []RouteMember `json:"members"`
	BeforeHash      string        `json:"beforeHash"`
	DesiredHash     string        `json:"desiredHash"`
	DesiredConfig   string        `json:"desiredConfig"`
	Added           []string      `json:"added"`
	Removed         []string      `json:"removed"`
	State           string        `json:"state"`
	CreatedAt       time.Time     `json:"createdAt"`
}
type Endpoint struct {
	Automation   *RouteAutomation      `json:"automation,omitempty"`
	Excluded     []RouteMember         `json:"excluded"`
	Verification *routing.Verification `json:"verification,omitempty"`
	Name         string                `json:"name"`
	Component    string                `json:"component"`
	URL          string                `json:"url"`
	Status       string                `json:"status"`
	Message      string                `json:"message,omitempty"`
	Current      []string              `json:"current"`
	Desired      []string              `json:"desired"`
}
type RouteView struct {
	Endpoints    []Endpoint  `json:"endpoints"`
	Publications []RoutePlan `json:"publications"`
}

func routeOwner(ctx context.Context) (domain.Principal, error) {
	p, ok := domain.Identity(ctx)
	if !ok || p.Role != "owner" || p.Kind != "session" || !p.Operates("") {
		return p, errors.New("route publication requires a human workspace owner")
	}
	return p, nil
}
func (c *Controller) routeBinding(app, name string) (routing.Binding, error) {
	for _, b := range c.RouteBindings {
		if b.ApplicationID == app && b.Name == name {
			return b, nil
		}
	}
	return routing.Binding{}, errors.New("native route is not configured for this application")
}
func (c *Controller) routeLock(ctx context.Context, app string) (*pgx.Conn, error) {
	conn, e := pgx.ConnectConfig(ctx, c.Store.Pool.Config().ConnConfig.Copy())
	if e != nil {
		return nil, e
	}
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", app).Scan(&locked); e != nil || !locked {
		conn.Close(ctx)
		return nil, domain.ErrConflict
	}
	return conn, nil
}
func closeRouteLock(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn.Close(ctx)
}
func routeHash(value any) string { raw, _ := json.Marshal(value); return routing.Hash(raw) }
func (c *Controller) routeSnapshot(ctx context.Context, appID string, b routing.Binding) (RoutePlan, map[string]bool, error) {
	app, e := c.Store.Application(ctx, appID)
	if e != nil {
		return RoutePlan{}, nil, e
	}
	target, e := c.Store.Target(ctx, app.Manifest.TargetID)
	if e != nil {
		return RoutePlan{}, nil, e
	}
	if b.TargetID != target.ID || target.Operator != "coolify" || target.Environment != "staging" || app.Manifest.Environment != "staging" {
		return RoutePlan{}, nil, errors.New("native route publication currently supports explicitly configured Coolify staging only")
	}
	releases, e := c.Store.Deployments(ctx, appID)
	if e != nil {
		return RoutePlan{}, nil, e
	}
	for _, r := range releases {
		if r.State == "queued" || r.State == "running" || r.State == "attention" || (r.State == "abandoned" && (r.Control == nil || r.Control.ResolvedAt == nil)) {
			return RoutePlan{}, nil, errors.New("resolve active or uncertain application work before reviewing routing")
		}
	}
	var count int
	for _, comp := range app.Manifest.Components {
		if comp.Name == b.Component {
			if comp.Management != "" || comp.ResourceID != "" || comp.Kind != "web" {
				return RoutePlan{}, nil, errors.New("shared routes require OAP-owned managed image web components")
			}
			count = comp.Instances
		}
	}
	if count < 1 {
		return RoutePlan{}, nil, errors.New("route component is unavailable")
	}
	instances, e := c.Instances(ctx, appID)
	if e != nil {
		return RoutePlan{}, nil, e
	}
	members := []RouteMember{}
	allowed := map[string]bool{}
	desired := []string{}
	active := map[int]bool{}
	for _, i := range instances {
		if i.Component != b.Component {
			continue
		}
		r := i.Resource
		if r == nil || r.ID != i.ResourceID || r.Description != "OpenAppPlatform:"+appID+":"+b.Component || r.ArtifactKind != "image" || !routing.Origin(r.URL, b.Domain) {
			return RoutePlan{}, nil, errors.New("backend ownership or origin cannot be verified")
		}
		endpoint := strings.TrimRight(r.URL, "/")
		if allowed[endpoint] {
			return RoutePlan{}, nil, errors.New("backend origins must be unique")
		}
		allowed[endpoint] = true
		members = append(members, RouteMember{Ordinal: i.Ordinal, ResourceID: i.ResourceID, URL: endpoint, Retired: i.Retired, Excluded: !i.Retired && r.Status != "running:healthy", Status: r.Status})
		if !i.Retired {
			if i.Ordinal < 1 || i.Ordinal > count || active[i.Ordinal] {
				return RoutePlan{}, nil, errors.New("desired backend ordinals must be unique and within the replica count")
			}
			active[i.Ordinal] = true
			if r.Status == "running:healthy" {
				desired = append(desired, endpoint)
			}
		}
	}
	if len(active) != count {
		return RoutePlan{}, nil, errors.New("desired backend membership is incomplete")
	}
	if len(desired) == 0 {
		return RoutePlan{}, nil, errors.New("no healthy backends; the existing route is preserved")
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Ordinal < members[j].Ordinal })
	config, e := routing.Render(b, desired)
	if e != nil {
		return RoutePlan{}, nil, e
	}
	// Include the server-only credential reference and configured file authority,
	// without returning either value or runtime variable values to the client.
	authority := routeHash(struct {
		App        domain.Application
		Target     domain.Target
		Credential string
		Binding    routing.Binding
		Members    []RouteMember
	}{app, target, target.TokenEnv, b, members})
	return RoutePlan{Name: b.Name, Component: b.Component, URL: "https://" + b.Domain, ExpectedVersion: app.Version, AuthorityHash: authority, Members: members, DesiredHash: routing.Hash(config), DesiredConfig: string(config), Added: []string{}, Removed: []string{}}, allowed, nil
}
func difference(left, right []string) []string {
	set := map[string]bool{}
	for _, v := range right {
		set[v] = true
	}
	out := []string{}
	for _, v := range left {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}
func desiredURLs(p RoutePlan) []string {
	out := []string{}
	for _, m := range p.Members {
		if !m.Retired && !m.Excluded {
			out = append(out, m.URL)
		}
	}
	return out
}
func (c *Controller) Routes(ctx context.Context, appID string) (RouteView, error) {
	if _, e := c.Store.Application(ctx, appID); e != nil {
		return RouteView{}, e
	}
	view := RouteView{Endpoints: []Endpoint{}, Publications: []RoutePlan{}}
	for _, b := range c.RouteBindings {
		if b.ApplicationID != appID {
			continue
		}
		endpoint := Endpoint{Name: b.Name, Component: b.Component, URL: "https://" + b.Domain, Status: "blocked", Excluded: []RouteMember{}, Current: []string{}, Desired: []string{}}
		snapshot, allowed, e := c.routeSnapshot(ctx, appID, b)
		authority, native, directory := "", "", ""
		if e == nil {
			endpoint.Desired = desiredURLs(snapshot)
			for _, member := range snapshot.Members {
				if member.Excluded {
					endpoint.Excluded = append(endpoint.Excluded, member)
				}
			}
			var f *routing.File
			f, e = routing.Open(b)
			if e == nil {
				var raw []byte
				raw, e = f.Read()
				if e == nil {
					directory, e = f.Identity()
					native = routing.Hash(raw)
					authority = snapshot.AuthorityHash
				}
				f.Close()
				if e == nil {
					endpoint.Current, e = routing.Backends(b, raw, allowed)
					if e == nil {
						endpoint.Status = "in-sync"
						if len(difference(endpoint.Current, endpoint.Desired))+len(difference(endpoint.Desired, endpoint.Current)) > 0 {
							endpoint.Status = "update-needed"
						}
					}
				}
			}
		}
		if e != nil {
			endpoint.Message = e.Error()
		}
		verification, err := c.endpointVerification(ctx, appID, b.Name, authority, native, directory)
		if err != nil {
			return view, err
		}
		endpoint.Verification = verification
		auto, autoErr := db.New(c.Store.Pool).GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: appID, Name: b.Name})
		if autoErr == nil {
			endpoint.Automation = automationView(auto)
		} else if !errors.Is(autoErr, pgx.ErrNoRows) {
			return view, autoErr
		}
		view.Endpoints = append(view.Endpoints, endpoint)
	}
	rows, e := db.New(c.Store.Pool).ListRoutePublications(ctx, appID)
	if e != nil {
		return view, e
	}
	for _, row := range rows {
		var p RoutePlan
		if e = json.Unmarshal(row.Plan, &p); e != nil {
			return view, e
		}
		p.State = row.State
		p.CreatedAt = row.CreatedAt.Time
		view.Publications = append(view.Publications, p)
	}
	return view, nil
}
func (c *Controller) ReviewRoute(ctx context.Context, appID, name string, version int64) (RoutePlan, error) {
	owner, e := routeOwner(ctx)
	if e != nil {
		return RoutePlan{}, e
	}
	b, e := c.routeBinding(appID, name)
	if e != nil {
		return RoutePlan{}, e
	}
	conn, e := c.routeLock(ctx, appID)
	if e != nil {
		return RoutePlan{}, e
	}
	defer closeRouteLock(conn)
	queries := db.New(conn)
	unresolved, e := queries.HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: appID})
	if e != nil {
		return RoutePlan{}, e
	}
	if unresolved {
		return RoutePlan{}, errors.New("resume or cancel the unresolved routing publication first")
	}
	auto, autoErr := queries.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: appID, Name: name})
	if autoErr != nil && !errors.Is(autoErr, pgx.ErrNoRows) {
		return RoutePlan{}, autoErr
	}
	if autoErr == nil && auto.Enabled {
		return RoutePlan{}, errors.New("disable automatic membership updates before reviewing a manual change")
	}
	plan, allowed, e := c.routeSnapshot(ctx, appID, b)
	if e != nil {
		return plan, e
	}
	if version != plan.ExpectedVersion {
		return plan, domain.ErrConflict
	}
	f, e := routing.Open(b)
	if e != nil {
		return plan, e
	}
	defer f.Close()
	current, e := f.Read()
	if e != nil {
		return plan, e
	}
	urls, e := routing.Backends(b, current, allowed)
	if e != nil {
		return plan, e
	}
	plan.DirectoryHash, e = f.Identity()
	if e != nil {
		return plan, e
	}
	plan.ID = domain.NewID()
	plan.BeforeHash = routing.Hash(current)
	plan.Added = difference(desiredURLs(plan), urls)
	plan.Removed = difference(urls, desiredURLs(plan))
	plan.State = "prepared"
	plan.CreatedAt = time.Now().UTC()
	raw, _ := json.Marshal(plan)
	e = queries.InsertRoutePublication(ctx, db.InsertRoutePublicationParams{ID: plan.ID, ApplicationID: appID, OwnerID: owner.UserID, Plan: raw})
	return plan, e
}
func (c *Controller) ApplyRoute(ctx context.Context, appID, id string, cancel bool) (RoutePlan, error) {
	owner, e := routeOwner(ctx)
	if e != nil {
		return RoutePlan{}, e
	}
	conn, e := c.routeLock(ctx, appID)
	if e != nil {
		return RoutePlan{}, e
	}
	defer closeRouteLock(conn)
	queries := db.New(conn)
	row, e := queries.GetRoutePublication(ctx, db.GetRoutePublicationParams{ApplicationID: appID, ID: id})
	if errors.Is(e, pgx.ErrNoRows) {
		return RoutePlan{}, domain.ErrNotFound
	}
	if e != nil {
		return RoutePlan{}, e
	}
	var plan RoutePlan
	if e = json.Unmarshal(row.Plan, &plan); e != nil {
		return plan, e
	}
	plan.State = row.State
	if row.OwnerID != owner.UserID {
		return plan, errors.New("only the owner who reviewed this plan can apply it")
	}
	if row.State == "applied" && !cancel || row.State == "cancelled" && cancel {
		return plan, nil
	}
	if row.State == "cancelled" || row.State == "applied" {
		return plan, domain.ErrConflict
	}
	if !cancel && row.State == "prepared" {
		auto, err := queries.GetRouteAutomation(ctx, db.GetRouteAutomationParams{ApplicationID: appID, Name: plan.Name})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return plan, err
		}
		if err == nil && auto.Enabled {
			return plan, errors.New("disable automatic updates before manual publication")
		}
	}
	b, e := c.routeBinding(appID, plan.Name)
	if e != nil {
		return plan, e
	}
	f, e := routing.Open(b)
	if e != nil {
		return plan, e
	}
	defer f.Close()
	current, e := f.Read()
	if e != nil {
		return plan, e
	}
	identity, e := f.Identity()
	if e != nil {
		return plan, e
	}
	if identity != plan.DirectoryHash {
		return plan, errors.New("native directory identity changed; inspect configured routing authority")
	}
	if cancel {
		// Cancelling a never-dispatched plan needs no native compensation; an
		// uncertain publication can be unfenced only if the reviewed before bytes remain.
		if row.State == "publishing" && routing.Hash(current) != plan.BeforeHash {
			return plan, errors.New("native outcome changed; resume publication or inspect the native route")
		}
		plan.State = "cancelled"
		e = queries.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: appID, ID: id, State: plan.State})
		return plan, e
	}
	other, e := queries.HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: appID, ID: id})
	if e != nil {
		return plan, e
	}
	if other {
		return plan, domain.ErrConflict
	}
	fresh, _, e := c.routeSnapshot(ctx, appID, b)
	if e != nil {
		return plan, e
	}
	if fresh.AuthorityHash != plan.AuthorityHash || fresh.DesiredHash != plan.DesiredHash || fresh.DesiredConfig != plan.DesiredConfig {
		return plan, errors.New("routing review is stale; review the current application")
	}
	if routing.Hash(current) != plan.BeforeHash && !(row.State == "publishing" && routing.Hash(current) == plan.DesiredHash) {
		return plan, errors.New("native route drift detected; review the current route")
	}
	// The intent autocommits on the same dedicated lock connection. A lost process
	// leaves a persistent fence, while the proxy continues serving independently.
	if e = queries.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: appID, ID: id, State: "publishing"}); e != nil {
		return plan, e
	}
	if _, e = f.Publish(plan.BeforeHash, []byte(plan.DesiredConfig)); e != nil {
		return plan, fmt.Errorf("publication needs recovery: %w", e)
	}
	plan.State = "applied"
	e = queries.SetRoutePublicationState(ctx, db.SetRoutePublicationStateParams{ApplicationID: appID, ID: id, State: plan.State})
	return plan, e
}
