#!/usr/bin/env bash
# The rewrite's definition-of-done checks (docs/architecture/rewrite-plan.md, R0.4).
# REWRITE_BASE names the branch the rewrite started from (default origin/main).
set -euo pipefail
cd "$(dirname "$0")/.."

NEW_TREE=(./internal/core/... ./internal/engine/... ./internal/ports/... ./internal/adapters/...
  ./internal/service/... ./internal/surface/... ./cmd/oapd/... ./cmd/oapctl/... ./cmd/oap-ext-builder/...
  ./cmd/oap-ext-registry/... ./cmd/oap-ext-github/... ./tests/arch/...)
NEW_DIRS=(internal/core internal/engine internal/ports internal/adapters internal/service
  internal/surface cmd/oapd cmd/oapctl cmd/oap-ext-builder cmd/oap-ext-registry cmd/oap-ext-github tests/arch)
[ -n "$(find gen -name '*.go' -print -quit)" ] && NEW_TREE+=(./gen/...) && NEW_DIRS+=(gen)

step() { printf '\n== %s\n' "$1"; }

step gofmt
unformatted=$(gofmt -l "${NEW_DIRS[@]}")
if [ -n "$unformatted" ]; then echo "not gofmt-formatted:"; echo "$unformatted"; exit 1; fi
echo ok

step "build (whole module)"
go build ./...
echo ok

step "go vet (new tree)"
go vet "${NEW_TREE[@]}"
echo ok

step "go test -race (new tree, architecture rules)"
go test -race -count=1 "${NEW_TREE[@]}"

step "randomized and seeded tests, full counts without the race detector"
go test -count=1 -run '^TestQueueRandomized$' ./internal/adapters/sqlite
go test -count=1 -run '^(TestObserver|TestScaleDown)' ./internal/engine

step "frozen paths"
python3 -m unittest discover -s scripts -p 'test_rewrite_*.py' 2>&1 | tail -1
python3 scripts/rewrite_frozen_paths.py --base "${REWRITE_BASE:-origin/main}"

step "protos"
if [ -n "$(find proto -name '*.proto' -print -quit)" ]; then
  make gen >/dev/null
  .local/bin/buf lint
  if [ -n "$(git status --porcelain -- gen)" ]; then
    git status --short -- gen
    echo "generated code is stale or hand-edited; run make gen and commit"
    exit 1
  fi
  echo ok
else
  echo "no .proto files yet"
fi

printf '\nrewrite-check: all checks passed\n'
