package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"testing"
)

func TestReleaseClaimsIncludeTransitiveDependenciesAndConnections(t *testing.T) {
	release := domain.Deployment{Manifest: domain.Manifest{Components: []domain.Component{{Name: "web", DependsOn: []string{"api"}, Services: map[string]string{"CACHE": "cache"}}, {Name: "api", DependsOn: []string{"cache"}}, {Name: "cache"}, {Name: "unrelated"}}}, Steps: []domain.Step{{Component: "web", Ordinal: 1}, {Component: "web", Ordinal: 2}}}
	claims, err := releaseClaims(release)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 3 || claims["web"] != "write" || claims["api"] != "read" || claims["cache"] != "read" {
		t.Fatal(claims)
	}
	release.Steps = append(release.Steps, domain.Step{Component: "cache", Ordinal: 1})
	claims, err = releaseClaims(release)
	if err != nil || claims["cache"] != "write" {
		t.Fatal("read downgraded writer", claims, err)
	}
}
