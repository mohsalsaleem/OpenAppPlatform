package docker

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ingress"
	"net"
	"strconv"
	"strings"
	"time"
)

// IngressSettings is explicit host-owner authority for an already provisioned,
// dedicated Caddy peer. No application can select its socket or native identity.
type IngressSettings struct {
	ContainerID   string   `json:"containerId"`
	AdminSocket   string   `json:"adminSocket"`
	ProxySocket   string   `json:"proxySocket"`
	Domain        string   `json:"domain"`
	Domains       []string `json:"domains,omitempty"`
	BootstrapHash string   `json:"bootstrapHash"`
	HTTPPort      int      `json:"httpPort"`
	HTTPSPort     int      `json:"httpsPort"`
	InternalCA    bool     `json:"internalCa"`
	PublicACME    string   `json:"publicAcme,omitempty"`
}

// AllowsDomain checks the explicit owner allowlist; no wildcard authority.
func (s IngressSettings) AllowsDomain(domain string) bool {
	if domain == s.Domain {
		return true
	}
	for _, allowed := range s.Domains {
		if allowed == domain {
			return true
		}
	}
	return false
}

// ACMECA is fixed host-owner policy, never an application-supplied issuer URL.
func (s IngressSettings) ACMECA() string {
	if s.PublicACME == "letsencrypt-staging" {
		return "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	return ""
}

func (c *Client) IngressSettings() (IngressSettings, bool) {
	if c.settings.Ingress == nil {
		return IngressSettings{}, false
	}
	return *c.settings.Ingress, true
}
func (c *Client) ingressPeer(ctx context.Context, m domain.Manifest) (container, error) {
	settings, ok := c.IngressSettings()
	if !ok || m.Ingress == nil || !settings.AllowsDomain(m.Ingress.Domain) {
		return container{}, errors.New("ingress domain is not authorized by the target owner")
	}
	if m.Ingress.HTTPS && !settings.InternalCA && settings.PublicACME == "" {
		return container{}, errors.New("HTTPS requires explicit owner internal-CA or public-ACME authority")
	}
	peer, err := c.inspectContainer(ctx, settings.ContainerID)
	if err != nil {
		return peer, errors.New("configured ingress peer is unavailable")
	}
	if peer.HostConfig.Privileged || peer.HostConfig.NetworkMode == "host" || len(peer.HostConfig.PortBindings["2019/tcp"]) > 0 {
		return peer, errors.New("ingress peer must isolate administration and host privileges")
	}
	if peer.ID != settings.ContainerID || peer.Config.Labels[prefix+"ingress-target"] != c.target.ID || !peer.State.Running {
		return peer, errors.New("configured ingress peer identity or ownership changed")
	}
	return peer, nil
}

// CheckIngressPeer is observation-only, including during recovery.
func (c *Client) CheckIngressPeer(ctx context.Context, app string, m domain.Manifest) error {
	peer, err := c.ingressPeer(ctx, m)
	if err != nil {
		return err
	}
	if len(app) != 32 {
		return errors.New("invalid ingress application identity")
	}
	network := "oap-" + c.target.ID + "-" + app[:12]
	if peer.NetworkSettings.Networks[network].IPAddress == "" {
		return errors.New("ingress peer application-network membership changed")
	}
	return nil
}

// AttachIngressPeer is permitted only before publication preparation. A recorded
// or uncertain publication cannot silently recreate its network relationship.
func (c *Client) AttachIngressPeer(ctx context.Context, app string, m domain.Manifest) error {
	peer, err := c.ingressPeer(ctx, m)
	if err != nil {
		return err
	}
	network, err := c.network(ctx, "OpenAppPlatform:"+app+":ingress")
	if err != nil {
		return err
	}
	if peer.NetworkSettings.Networks[network].IPAddress != "" {
		return nil
	}
	settings, _ := c.IngressSettings()
	if err = c.request(ctx, "POST", "/networks/"+network+"/connect", map[string]any{"Container": settings.ContainerID}, nil); err != nil {
		return errors.New("ingress peer network attachment unavailable")
	}
	return nil
}

// IngressMembers inspects exact binding references and verifies native ownership
// and network membership; agent-provided URLs never enter the route compiler.
func (c *Client) IngressMembers(ctx context.Context, app string, m domain.Manifest, references map[string]string) ([]ingress.Member, error) {
	out := []ingress.Member{}
	if m.Ingress == nil {
		return out, nil
	}
	needed := map[string]bool{}
	for _, r := range m.Ingress.Routes {
		needed[r.Component] = true
	}
	expectedNetwork := "oap-" + c.target.ID + "-" + app[:12]
	for _, component := range m.Components {
		if !needed[component.Name] {
			continue
		}
		for ordinal := 1; ordinal <= component.Instances; ordinal++ {
			key := component.Name + "/" + strconv.Itoa(ordinal)
			ref := references[key]
			if ref == "" {
				return nil, errors.New("ingress instance binding missing")
			}
			native, err := c.inspectContainer(ctx, ref)
			if err != nil {
				return nil, errors.New("ingress runtime observation unavailable")
			}
			if !c.owned(native) || native.Config.Labels[prefix+"owner"] != "OpenAppPlatform:"+app+":"+component.Name || native.Config.Labels[prefix+"reference"] != ref {
				return nil, errors.New("ingress runtime binding ownership changed")
			}
			if native.Config.Labels[prefix+"port"] != strconv.Itoa(component.Port) {
				return nil, errors.New("ingress native port differs from declared component")
			}
			ip := native.NetworkSettings.Networks[expectedNetwork].IPAddress
			if ip == "" || net.ParseIP(ip) == nil {
				return nil, errors.New("ingress instance is outside its application network")
			}
			ready := native.State.Running && native.State.Health != nil && native.State.Health.Status == "healthy"
			out = append(out, ingress.Member{ApplicationID: app, TargetID: m.TargetID, Component: component.Name, Ordinal: ordinal, Address: net.JoinHostPort(ip, strconv.Itoa(component.Port)), Ready: ready, ObservedAt: time.Now()})
		}
	}
	return out, nil
}
func validateIngressSettings(s *IngressSettings) error {
	if s == nil {
		return nil
	}
	if (s.PublicACME != "" && s.PublicACME != "letsencrypt" && s.PublicACME != "letsencrypt-staging") || (s.InternalCA && s.PublicACME != "") {
		return errors.New("ingress requires one explicit supported TLS authority")
	}
	if len(s.ContainerID) != 64 || strings.Trim(s.ContainerID, "0123456789abcdef") != "" || len(s.BootstrapHash) != 64 || strings.Trim(s.BootstrapHash, "0123456789abcdef") != "" {
		return errors.New("ingress requires exact owner-configured peer and bootstrap hashes")
	}
	if s.Domain == "" || s.HTTPPort < 1 || s.HTTPPort > 65535 || s.HTTPSPort < 1 || s.HTTPSPort > 65535 || s.HTTPPort == s.HTTPSPort {
		return errors.New("invalid owner ingress listener configuration")
	}
	check := domain.IngressDefinition{Domain: s.Domain, Routes: []domain.IngressRoute{{Path: "/", Component: "probe"}}}
	if err := check.Validate(domain.Manifest{Components: []domain.Component{{Name: "probe", Kind: "web", Port: 80}}}); err != nil {
		return err
	}
	if len(s.Domains) > 31 {
		return errors.New("ingress permits at most 32 owner domains")
	}
	seen := map[string]bool{s.Domain: true}
	for _, host := range s.Domains {
		if seen[host] {
			return errors.New("duplicate owner ingress domain")
		}
		seen[host] = true
		check.Domain = host
		if err := check.Validate(domain.Manifest{Components: []domain.Component{{Name: "probe", Kind: "web", Port: 80}}}); err != nil {
			return err
		}
	}
	client, err := ingress.New(s.AdminSocket)
	if err != nil {
		return err
	}
	client.Close()
	if !strings.HasPrefix(s.ProxySocket, "/run/oap-ingress/") || strings.ContainsAny(s.ProxySocket, "|\r\n") || strings.Contains(s.ProxySocket, "..") {
		return errors.New("ingress proxy socket must be private")
	}
	return nil
}

func (c *Client) ValidateManifest(m domain.Manifest) error {
	if m.Ingress == nil {
		return nil
	}
	settings, ok := c.IngressSettings()
	if !ok || !settings.AllowsDomain(m.Ingress.Domain) {
		return errors.New("ingress domain is not authorized by the target owner")
	}
	if m.Ingress.HTTPS && !settings.InternalCA && settings.PublicACME == "" {
		return errors.New("HTTPS requires explicit owner internal-CA or public-ACME authority")
	}
	return nil
}
