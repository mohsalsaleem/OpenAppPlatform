package service

import (
	"context"
	"errors"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/policy"
)

// Change facts (R4.3). A deploy whose source is known carries facts about
// that commit from the fact source (a GitHub App extension, for example),
// and the envelope's change_facts rules judge them: CI not green, a
// migration or lockfile changed, docs only, unreviewed, unsigned. The
// source is the deploy's own, or, for an image an OAP build made, the
// commit that build recorded. When the fact source can't answer, CI is
// "unknown", never green.

// changeFacts returns the facts for a deploy intent, or nil when it has
// no known source or the server has no fact source.
func (s *Service) changeFacts(ctx context.Context, in *oapv1.Intent) *oapv1.ChangeFacts {
	d := in.GetDeploy()
	if d == nil || s.Facts == nil {
		return nil
	}
	repo, commit, pr := d.GetSource().GetRepository(), d.GetSource().GetCommit(), int(d.GetSource().GetPullRequest())
	if repo == "" || commit == "" {
		repo, commit = s.builtFrom(ctx, in.GetApplication(), d.GetImages())
		if repo == "" {
			return nil
		}
	}
	f, err := s.Facts.Facts(ctx, ops.FactsRequest{Repository: repo, Commit: commit, PullRequest: pr})
	switch {
	case errors.Is(err, ops.ErrNoFactSource):
		return nil
	case err != nil:
		return &oapv1.ChangeFacts{Repository: repo, Commit: commit, Ci: "unknown"}
	}
	f.MigrationsChanged, f.LockfileChanged, f.DocsOnly = policy.Classify(f.GetFilesChanged())
	return f
}

// builtFrom finds the repository and commit an OAP build of these images
// came from: the newest build outcome that produced one of them.
func (s *Service) builtFrom(ctx context.Context, app string, images map[string]string) (repo, commit string) {
	entries, err := s.entries(ctx, 0)
	if err != nil {
		return "", ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind != KindOutcome {
			continue
		}
		je, err := decode(entries[i].Data)
		if err != nil {
			continue
		}
		o := je.GetOutcome()
		for comp, img := range images {
			if o.GetImages()[comp] != img || o.GetCommits()[comp] == "" {
				continue
			}
			for _, e := range entries {
				if e.Kind == KindApply && e.Operation == o.GetOperationId() {
					aj, _ := decode(e.Data)
					p, err := s.proposal(ctx, aj.GetParentId())
					if err == nil && p.GetIntent().GetApplication() == app && p.GetIntent().GetBuild() != nil {
						return p.GetIntent().GetBuild().GetRepository(), o.GetCommits()[comp]
					}
				}
			}
		}
	}
	return "", ""
}
