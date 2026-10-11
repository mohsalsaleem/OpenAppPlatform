package controller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/routing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
	"time"
)

func (c *Controller) VerifyEndpoint(ctx context.Context, app, name string, version int64) (routing.Verification, error) {
	if _, e := routeOwner(ctx); e != nil {
		return routing.Verification{}, e
	}
	b, e := c.routeBinding(app, name)
	if e != nil {
		return routing.Verification{}, e
	}
	conn, e := c.routeLock(ctx, app)
	if e != nil {
		return routing.Verification{}, e
	}
	defer closeRouteLock(conn)
	unresolved, e := db.New(conn).HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: app})
	if e != nil {
		return routing.Verification{}, e
	}
	if unresolved {
		return routing.Verification{}, domain.ErrConflict
	}
	snapshot, allowed, e := c.routeSnapshot(ctx, app, b)
	if e != nil {
		return routing.Verification{}, e
	}
	if snapshot.ExpectedVersion != version {
		return routing.Verification{}, domain.ErrConflict
	}
	f, e := routing.Open(b)
	if e != nil {
		return routing.Verification{}, e
	}
	defer f.Close()
	raw, e := f.Read()
	if e != nil {
		return routing.Verification{}, e
	}
	current, e := routing.Backends(b, raw, allowed)
	if e != nil {
		return routing.Verification{}, e
	}
	if len(difference(current, desiredURLs(snapshot)))+len(difference(desiredURLs(snapshot), current)) > 0 {
		return routing.Verification{}, errors.New("Apply current backend membership before verifying this endpoint")
	}
	directory, e := f.Identity()
	if e != nil {
		return routing.Verification{}, e
	}
	probe := c.EndpointProbe
	if probe == nil {
		probe = routing.Probe
	}
	report := probe(ctx, b)
	report.AuthorityHash = snapshot.AuthorityHash
	report.NativeHash = routing.Hash(raw)
	report.DirectoryHash = directory
	fresh, _, e := c.routeSnapshot(ctx, app, b)
	if e != nil {
		return routing.Verification{}, e
	}
	after, e := f.Read()
	if e != nil {
		return routing.Verification{}, e
	}
	if fresh.AuthorityHash != snapshot.AuthorityHash || routing.Hash(after) != report.NativeHash {
		return routing.Verification{}, domain.ErrConflict
	}
	// Reopen to detect a replaced mount/path, rather than merely checking the
	// original pinned descriptor a second time.
	reopened, e := routing.Open(b)
	if e != nil {
		return routing.Verification{}, e
	}
	newIdentity, e := reopened.Identity()
	reopened.Close()
	if e != nil || newIdentity != directory {
		return routing.Verification{}, domain.ErrConflict
	}
	payload, e := json.Marshal(report)
	if e != nil {
		return report, e
	}
	e = db.New(conn).SaveEndpointVerification(ctx, db.SaveEndpointVerificationParams{ApplicationID: app, Name: name, Report: payload})
	return report, e
}
func (c *Controller) endpointVerification(ctx context.Context, app, name, authority, native, directory string) (*routing.Verification, error) {
	raw, e := db.New(c.Store.Pool).GetEndpointVerification(ctx, db.GetEndpointVerificationParams{ApplicationID: app, Name: name})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var report routing.Verification
	if e = json.Unmarshal(raw, &report); e != nil {
		return nil, e
	}
	report = routing.CurrentVerification(report, authority, native, directory, time.Now().UTC())
	return &report, nil
}
