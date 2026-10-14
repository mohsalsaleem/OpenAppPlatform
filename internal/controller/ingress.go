package controller

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ingress"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"strconv"
	"time"
)

// publishIngress journals intent before one native conditional write. Unknown
// writes are observation-only; a mismatched native config never gets overwritten.
func (c *Controller) publishIngress(ctx context.Context, d domain.Deployment) (bool, error) {
	adapter, err := c.Adapter(ctx, d.Manifest.TargetID)
	if err != nil {
		return false, err
	}
	native, ok := adapter.(*docker.Client)
	if !ok {
		return false, errors.New("application ingress requires the standalone Docker adapter")
	}
	settings, ok := native.IngressSettings()
	if !ok {
		return false, errors.New("target ingress is disabled")
	}
	lock, err := c.routeLock(ctx, "ingress:"+d.Manifest.TargetID)
	if err != nil {
		return false, err
	}
	defer closeRouteLock(lock)
	if err = c.authorizeRelease(ctx, d); err != nil {
		return false, err
	}
	peerManifest, err := c.ingressPeerManifest(ctx, d)
	if err != nil {
		return false, err
	}
	target, err := c.Store.Target(ctx, d.Manifest.TargetID)
	if err != nil {
		return false, err
	}
	authority := store.HashJSON(target)
	client, err := ingress.New(settings.AdminSocket)
	if err != nil {
		return false, err
	}
	defer client.Close()
	publication, err := c.Store.IngressPublication(ctx, d.ID)
	if errors.Is(err, domain.ErrNotFound) {
		var e error
		if d.Manifest.Ingress == nil {
			e = native.CheckIngressPeer(ctx, d.ApplicationID, peerManifest)
		} else {
			e = native.AttachIngressPeer(ctx, d.ApplicationID, peerManifest)
		}
		if e != nil {
			return false, e
		}
		bindings, e := c.Store.Bindings(ctx, d.ApplicationID)
		if e != nil {
			return false, e
		}
		references := map[string]string{}
		for _, b := range bindings {
			if !b.Retired {
				references[b.Component+"/"+strconv.Itoa(b.Ordinal)] = b.ResourceID
			}
		}
		members, e := native.IngressMembers(ctx, d.ApplicationID, d.Manifest, references)
		if e != nil {
			return false, e
		}
		candidate, e := c.compileIngress(ctx, native, d, members, settings)
		if e != nil {
			return false, e
		}
		snapshot, e := client.Read(ctx)
		if e != nil {
			return false, e
		}
		expected, e := c.Store.IngressExpected(ctx, d.Manifest.TargetID, d.ApplicationID, authority, settings.BootstrapHash)
		if e != nil {
			return false, e
		}
		if snapshot.Hash != expected {
			return false, ingress.ErrConflict
		}
		publication = store.IngressPublication{DeploymentID: d.ID, ApplicationID: d.ApplicationID, TargetID: d.Manifest.TargetID, AuthorityHash: authority, ExpectedHash: snapshot.Hash, ExpectedETag: snapshot.ETag, CandidateHash: candidate.Hash(), Config: candidate.JSON(), Phase: "prepared"}
		if e = c.Store.PrepareIngress(ctx, publication); e != nil {
			return false, e
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = native.CheckIngressPeer(ctx, d.ApplicationID, peerManifest); err != nil {
		return false, err
	}
	if publication.ApplicationID != d.ApplicationID || publication.TargetID != d.Manifest.TargetID || publication.AuthorityHash != authority {
		return false, ingress.ErrConflict
	}
	observed, err := client.Read(ctx)
	if err != nil {
		return false, err
	}
	if observed.Hash == publication.CandidateHash {
		if err = c.Store.ConfirmIngress(ctx, publication); err != nil {
			return false, err
		}
		return true, nil
	}
	if publication.Phase != "prepared" {
		return false, ingress.ErrUnknown
	}
	if observed.Hash != publication.ExpectedHash || observed.ETag != publication.ExpectedETag {
		return false, ingress.ErrConflict
	}
	if err = c.authorizeRelease(ctx, d); err != nil {
		return false, err
	}
	// Revalidate exact owned membership immediately before dispatch; stale runtime
	// identities or addresses invalidate the frozen candidate, never rewrite it.
	bindings, err := c.Store.Bindings(ctx, d.ApplicationID)
	if err != nil {
		return false, err
	}
	refs := map[string]string{}
	for _, b := range bindings {
		if !b.Retired {
			refs[b.Component+"/"+strconv.Itoa(b.Ordinal)] = b.ResourceID
		}
	}
	members, err := native.IngressMembers(ctx, d.ApplicationID, d.Manifest, refs)
	if err != nil {
		return false, err
	}
	current, err := c.compileIngress(ctx, native, d, members, settings)
	if err != nil {
		return false, err
	}
	if current.Hash() != publication.CandidateHash {
		return false, ingress.ErrConflict
	}
	candidate, err := ingress.RestoreCandidate(publication.Config, publication.CandidateHash)
	if err != nil {
		return false, err
	}
	if err = c.Store.DispatchIngress(ctx, d.ID); err != nil {
		return false, err
	}
	receipt, err := client.Apply(ctx, ingress.Snapshot{Hash: publication.ExpectedHash, ETag: publication.ExpectedETag}, candidate)
	if err != nil {
		return false, err
	}
	if receipt.State != "applied" && receipt.State != "unchanged" {
		return false, ingress.ErrUnknown
	}
	if err = c.Store.ConfirmIngress(ctx, publication); err != nil {
		return false, err
	}
	return true, nil
}
func (c *Controller) ingressDrain(ctx context.Context, d domain.Deployment) (bool, error) {
	if (d.Operation != "scale-down" && d.Operation != "remove-component") || (d.Operation == "scale-down" && d.Manifest.Ingress == nil) {
		return true, nil
	}
	publication, err := c.Store.IngressPublication(ctx, d.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if publication.Phase != "applied" {
		return true, nil
	}
	if publication.DispatchedAt == nil {
		return false, errors.New("ingress withdrawal has no dispatch timestamp")
	}
	if time.Until(publication.DispatchedAt.Add(31*time.Second)) > 0 {
		return false, nil
	}
	adapter, err := c.Adapter(ctx, d.Manifest.TargetID)
	if err != nil {
		return false, err
	}
	native, ok := adapter.(*docker.Client)
	if !ok {
		return false, ingress.ErrConflict
	}
	target, err := c.Store.Target(ctx, d.Manifest.TargetID)
	if err != nil || store.HashJSON(target) != publication.AuthorityHash {
		return false, ingress.ErrConflict
	}
	peerManifest, err := c.ingressPeerManifest(ctx, d)
	if err != nil {
		return false, err
	}
	if err = native.CheckIngressPeer(ctx, d.ApplicationID, peerManifest); err != nil {
		return false, err
	}
	settings, _ := native.IngressSettings()
	client, err := ingress.New(settings.AdminSocket)
	if err != nil {
		return false, err
	}
	defer client.Close()
	observed, err := client.Read(ctx)
	if err != nil {
		return false, err
	}
	if observed.Hash != publication.CandidateHash {
		return false, ingress.ErrConflict
	}
	bindings, err := c.Store.Bindings(ctx, d.ApplicationID)
	if err != nil {
		return false, err
	}
	refs := map[string]string{}
	for _, binding := range bindings {
		if !binding.Retired {
			refs[binding.Component+"/"+strconv.Itoa(binding.Ordinal)] = binding.ResourceID
		}
	}
	members, err := native.IngressMembers(ctx, d.ApplicationID, d.Manifest, refs)
	if err != nil {
		return false, err
	}
	current, err := c.compileIngress(ctx, native, d, members, settings)
	if err != nil || current.Hash() != publication.CandidateHash {
		return false, errors.New("surviving ingress membership changed; retirement is held")
	}
	return true, nil
}

// recoverIngress confirms only the recorded candidate; it never sends another
// native write after uncertain dispatch. Preparation retry is separately fenced.
func (c *Controller) recoverIngress(ctx context.Context, d domain.Deployment, request Recovery) error {
	if request.RemoteDeploymentID != "" {
		return errors.New("ingress recovery uses its recorded configuration identity")
	}
	publication, err := c.Store.IngressPublication(ctx, d.ID)
	if errors.Is(err, domain.ErrNotFound) && request.RetryPreparation {
		return nil
	}
	if err != nil {
		return err
	}
	if request.RemoteDeploymentID != "" {
		return errors.New("ingress recovery uses its recorded configuration identity")
	}
	if request.RetryPreparation {
		if publication.Phase != "prepared" {
			return errors.New("dispatched ingress cannot be prepared again")
		}
		return nil
	}
	adapter, err := c.Adapter(ctx, d.Manifest.TargetID)
	if err != nil {
		return err
	}
	native, ok := adapter.(*docker.Client)
	if !ok {
		return ingress.ErrConflict
	}
	target, err := c.Store.Target(ctx, d.Manifest.TargetID)
	if err != nil || store.HashJSON(target) != publication.AuthorityHash {
		return ingress.ErrConflict
	}
	peerManifest, err := c.ingressPeerManifest(ctx, d)
	if err != nil {
		return err
	}
	if err = native.CheckIngressPeer(ctx, d.ApplicationID, peerManifest); err != nil {
		return err
	}
	settings, _ := native.IngressSettings()
	client, err := ingress.New(settings.AdminSocket)
	if err != nil {
		return err
	}
	defer client.Close()
	observed, err := client.Read(ctx)
	if err != nil {
		return err
	}
	if observed.Hash != publication.CandidateHash {
		return ingress.ErrUnknown
	}
	return c.Store.ConfirmIngress(ctx, publication)
}

// compileIngress rebuilds the entire target from durable published sibling
// definitions and fresh exact runtime bindings. Agents cannot supply siblings.
func (c *Controller) compileIngress(ctx context.Context, native *docker.Client, d domain.Deployment, members []ingress.Member, settings docker.IngressSettings) (ingress.Candidate, error) {
	siblings, err := c.Store.IngressSiblings(ctx, d.Manifest.TargetID, d.ApplicationID)
	if err != nil {
		return ingress.Candidate{}, err
	}
	applications := []ingress.ApplicationRoutes{}
	if d.Manifest.Ingress != nil {
		applications = append(applications, ingress.ApplicationRoutes{ApplicationID: d.ApplicationID, Manifest: d.Manifest, Members: members})
	}
	for _, sibling := range siblings {
		if sibling.Manifest.Ingress == nil {
			continue // The latest publication withdrew this sibling's routes.
		}
		if err = native.CheckIngressPeer(ctx, sibling.ApplicationID, sibling.Manifest); err != nil {
			return ingress.Candidate{}, err
		}
		bindings, e := c.Store.Bindings(ctx, sibling.ApplicationID)
		if e != nil {
			return ingress.Candidate{}, e
		}
		refs := map[string]string{}
		for _, binding := range bindings {
			if !binding.Retired {
				refs[binding.Component+"/"+strconv.Itoa(binding.Ordinal)] = binding.ResourceID
			}
		}
		observed, e := native.IngressMembers(ctx, sibling.ApplicationID, sibling.Manifest, refs)
		if e != nil {
			return ingress.Candidate{}, e
		}
		applications = append(applications, ingress.ApplicationRoutes{ApplicationID: sibling.ApplicationID, Manifest: sibling.Manifest, Members: observed})
	}
	return ingress.CompileServer(applications, ingress.Options{AdminSocket: settings.ProxySocket, HTTPPort: settings.HTTPPort, HTTPSPort: settings.HTTPSPort, InternalCA: settings.InternalCA, ACMECA: settings.ACMECA()}, time.Now())
}

// Withdrawal validates the exact peer/network using its frozen, confirmed
// predecessor. Desired ingress is nil, so it cannot supply this authority itself.
func (c *Controller) ingressPeerManifest(ctx context.Context, d domain.Deployment) (domain.Manifest, error) {
	if d.Manifest.Ingress != nil {
		return d.Manifest, nil
	}
	for _, step := range d.Steps {
		if step.Action != "ingress" || step.Component != "@ingress" || step.Ordinal != 1 || step.ResourceID == "" {
			continue
		}
		previous, err := c.Store.Deployment(ctx, step.ResourceID)
		if err != nil {
			return domain.Manifest{}, err
		}
		publication, err := c.Store.IngressPublication(ctx, previous.ID)
		if err != nil || previous.ApplicationID != d.ApplicationID || previous.Manifest.TargetID != d.Manifest.TargetID || previous.Manifest.Ingress == nil || publication.ApplicationID != d.ApplicationID || publication.TargetID != d.Manifest.TargetID || publication.Phase != "applied" {
			return domain.Manifest{}, ingress.ErrConflict
		}
		return previous.Manifest, nil
	}
	return domain.Manifest{}, errors.New("ingress withdrawal requires a confirmed predecessor")
}
