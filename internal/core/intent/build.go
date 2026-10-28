package intent

import (
	"fmt"
	"regexp"
	"strings"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// KindBuild builds a component's image (R4.2).
const KindBuild Kind = "build"

// Build defaults.
const (
	DefaultBuildMemoryMiB = 512
	buildImagePrefix      = "oap-build/"
)

var (
	repoURL  = regexp.MustCompile(`^(https?://|ssh://|git@|file://)[^\s'"]+$`)
	commitRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,128}$`)
	relPath  = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,256}$`)
)

// Kind is KindBuild.
func (*BuildResult) Kind() Kind { return KindBuild }

// BuildResult is a compiled build intent.
type BuildResult struct {
	Component string
	// Image is the name the build's result gets, without a tag.
	Image                                   string
	Repository, Commit, Context, Dockerfile string
	MemoryMiB                               int
	Builder                                 string
	Changes                                 []spec.Change
}

// CompileBuild checks a build intent against the application's desired
// state. A build changes no desired state.
func CompileBuild(current any, in *oapv1.Intent) (*BuildResult, *Error) {
	b := in.GetBuild()
	if b == nil {
		return nil, fail(WhyMissingChange, "change", "not a build intent")
	}
	tree, _ := current.(map[string]any)
	name, _ := tree["name"].(string)
	if in.GetApplication() != name {
		return nil, &Error{Why: WhyWrongApplication, Remedy: refresh,
			Message: fmt.Sprintf("intent is for %q, but this is %q", in.GetApplication(), name)}
	}
	components, _ := tree["components"].(map[string]any)
	if _, ok := components[b.GetComponent()]; !ok {
		return nil, fail(WhyUnknownComponent, "change.build.component", "%s has no component %q", name, b.GetComponent())
	}
	res := &BuildResult{Component: b.GetComponent(), Image: buildImagePrefix + name + "-" + b.GetComponent(),
		Repository: b.GetRepository(), Commit: b.GetCommit(), Context: b.GetContext(), Dockerfile: b.GetDockerfile(),
		MemoryMiB: int(b.GetMemoryMib()), Builder: b.GetBuilder()}
	if res.Context == "" {
		res.Context = "."
	}
	if res.Dockerfile == "" {
		res.Dockerfile = "Dockerfile"
	}
	if res.MemoryMiB == 0 {
		res.MemoryMiB = DefaultBuildMemoryMiB
	}
	switch {
	case !repoURL.MatchString(res.Repository):
		return nil, fail(WhyBadValue, "change.build.repository", "the repository is a git URL (https://, ssh://, git@ or file://)")
	case !commitRe.MatchString(res.Commit):
		return nil, fail(WhyBadValue, "change.build.commit", "name a commit, branch or tag")
	case !relPath.MatchString(res.Context) || strings.Contains(res.Context, ".."):
		return nil, fail(WhyBadValue, "change.build.context", "the context is a directory inside the repository")
	case !relPath.MatchString(res.Dockerfile) || strings.Contains(res.Dockerfile, ".."):
		return nil, fail(WhyBadValue, "change.build.dockerfile", "the Dockerfile is a path inside the context")
	case res.MemoryMiB < 64 || res.MemoryMiB > 8192:
		return nil, fail(WhyBadValue, "change.build.memory_mib", "a build may use 64 to 8192 MiB")
	}
	res.Changes = []spec.Change{{Path: "builds." + res.Component, Op: spec.OpAdd}}
	return res, nil
}
