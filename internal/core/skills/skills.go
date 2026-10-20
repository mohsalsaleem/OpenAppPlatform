// Package skills holds the guidance OAP serves to agents (R3.16): how to
// turn a repository of any stack into an image and an oap.yaml. oapd
// serves it over MCP and HTTPS, so an agent always reads the version that
// matches the server it works with; harness packages only point to it.
package skills

import (
	_ "embed"
)

//go:embed author-app.md
var authorApp string

// Skill is a guide for agents.
type Skill struct {
	Name, Title, Description, Markdown string
}

// All returns every skill, by name.
func All() []Skill {
	return []Skill{{
		Name:        "author-app",
		Title:       "Put an application on OAP",
		Description: "Turn a repository, whatever its stack, into a container image and an oap.yaml; declare secrets and ask the owner for their values; propose, apply and verify.",
		Markdown:    authorApp,
	}}
}

// Get returns a skill by name.
func Get(name string) (Skill, bool) {
	for _, s := range All() {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}
