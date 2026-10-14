package docker

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"testing"
)

func TestOwnerMustExplicitlyAuthorizePublicACME(t *testing.T) {
	settings := IngressSettings{ContainerID: strings.Repeat("a", 64), BootstrapHash: strings.Repeat("b", 64), AdminSocket: "/run/oap-ingress/admin.sock", ProxySocket: "/run/oap-ingress/admin.sock", Domain: "app.example.com", HTTPPort: 80, HTTPSPort: 443}
	manifest := domain.Manifest{Ingress: &domain.IngressDefinition{Domain: settings.Domain, HTTPS: true}}
	client := &Client{settings: Settings{Ingress: &settings}}
	if err := client.ValidateManifest(manifest); err == nil {
		t.Fatal("implicit public ACME admitted")
	}
	settings.PublicACME = "letsencrypt"
	if err := validateIngressSettings(&settings); err != nil {
		t.Fatal(err)
	}
	if err := client.ValidateManifest(manifest); err != nil {
		t.Fatal(err)
	}
	settings.PublicACME = "letsencrypt-staging"
	if settings.ACMECA() != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatal("wrong staging authority")
	}
	settings.InternalCA = true
	if err := validateIngressSettings(&settings); err == nil {
		t.Fatal("mixed CA authorities admitted")
	}
	settings.InternalCA = false
	settings.PublicACME = "https://untrusted.example/issuer"
	if err := validateIngressSettings(&settings); err == nil {
		t.Fatal("arbitrary issuer admitted")
	}
}

func TestOwnerIngressDomainAllowlist(t *testing.T) {
	s := IngressSettings{Domain: "one.example.test", Domains: []string{"two.example.test"}}
	for _, host := range []string{"one.example.test", "two.example.test"} {
		if !s.AllowsDomain(host) {
			t.Fatal("owner domain rejected", host)
		}
	}
	for _, host := range []string{"three.example.test", "*.example.test", "ONE.example.test", ""} {
		if s.AllowsDomain(host) {
			t.Fatal("unauthorized domain accepted", host)
		}
	}
}
