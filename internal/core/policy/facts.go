package policy

import (
	"path"
	"regexp"
	"strings"
)

// Classify derives change facts from the paths a commit changed (R4.3):
// whether it touches database migrations, dependency lockfiles, or only
// documentation. Fact sources report paths; the core decides what they
// are, the same way for every code host.
func Classify(files []string) (migrations, lockfile, docsOnly bool) {
	docsOnly = len(files) > 0
	for _, f := range files {
		f = strings.TrimPrefix(path.Clean("/"+f), "/")
		if isMigration(f) {
			migrations = true
		}
		if lockfiles[path.Base(f)] {
			lockfile = true
		}
		if !isDoc(f) {
			docsOnly = false
		}
	}
	return migrations, lockfile, docsOnly
}

var lockfiles = map[string]bool{
	"go.sum": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lock": true, "bun.lockb": true, "Gemfile.lock": true, "poetry.lock": true, "uv.lock": true, "Pipfile.lock": true,
	"Cargo.lock": true, "composer.lock": true, "mix.lock": true, "pubspec.lock": true, "packages.lock.json": true,
	"gradle.lockfile": true, "Package.resolved": true, "flake.lock": true,
}

var flyway = regexp.MustCompile(`^V[0-9][0-9._]*__.+\.sql$`)

func isMigration(f string) bool {
	for _, seg := range strings.Split(path.Dir(f), "/") {
		if seg == "migrations" || seg == "migrate" {
			return true
		}
	}
	return strings.Contains("/"+f, "/alembic/versions/") || flyway.MatchString(path.Base(f))
}

var docExt = map[string]bool{".md": true, ".mdx": true, ".markdown": true, ".rst": true, ".adoc": true, ".txt": true}

func isDoc(f string) bool {
	if docExt[strings.ToLower(path.Ext(f))] {
		return true
	}
	for _, seg := range strings.Split(path.Dir(f), "/") {
		if seg == "docs" || seg == "doc" {
			return true
		}
	}
	base := strings.ToUpper(path.Base(f))
	for _, p := range []string{"LICENSE", "LICENCE", "CHANGELOG", "AUTHORS", "NOTICE"} {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}
