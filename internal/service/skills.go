package service

import (
	"context"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/skills"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// Skill is a guide OAP serves to agents (R3.16).
type Skill = skills.Skill

// Why code for an unknown skill.
const WhyUnknownSkill = "skill.unknown"

// Skills lists the guides for agents. Any signed-in caller may read them.
func (s *Service) Skills(ctx context.Context, c Caller) []Skill {
	return skills.All()
}

// Skill returns one guide.
func (s *Service) Skill(ctx context.Context, c Caller, name string) (Skill, error) {
	sk, ok := skills.Get(name)
	if !ok {
		return Skill{}, fail(NotFound, WhyUnknownSkill, "no skill %q", name)
	}
	return sk, nil
}

// ApplicationSchema is the JSON Schema of oap.yaml.
func (s *Service) ApplicationSchema(ctx context.Context, c Caller) []byte {
	return spec.ApplicationSchema
}
