#!/bin/sh
# Builds a release of OAP (R5.6, docs/architecture/release-plan.md):
#   oapctl for macOS and Linux (amd64, arm64), with the version and the oapd
#   image it installs pinned by digest;
#   the oapd image (with its first-party extensions) for linux/amd64 and
#   linux/arm64, pushed to $IMAGE_REPO;
#   SHA256SUMS over every file; a draft GitHub release.
# Usage: scripts/release.sh v0.2.0[-rc.N]
# DRY_RUN=1 builds everything locally (the image as an OCI archive) and
#   publishes nothing. SKIP_CHECK=1 skips make rewrite-check.
# LOCAL_REGISTRY=1 (tests) pushes the image to IMAGE_REPO through BUILDER
#   without logging in, and makes no GitHub release.
set -eu
cd "$(dirname "$0")/.."

VERSION=${1:?usage: scripts/release.sh vX.Y.Z[-rc.N]}
echo "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$' || { echo "release: $VERSION isn't vX.Y.Z or vX.Y.Z-rc.N" >&2; exit 2; }
IMAGE_REPO=${IMAGE_REPO:-ghcr.io/mohsalsaleem/oapd}
DRY_RUN=${DRY_RUN:-}
LOCAL_REGISTRY=${LOCAL_REGISTRY:-}
BUILDER=${BUILDER:-oap-release}
DIST=dist/$VERSION
COMMIT=$(git rev-parse HEAD)

if [ -z "$DRY_RUN" ] && [ -z "$LOCAL_REGISTRY" ]; then
  [ -z "$(git status --porcelain)" ] || { echo "release: the working tree isn't clean" >&2; exit 2; }
  git merge-base --is-ancestor "$COMMIT" "@{upstream}" 2>/dev/null || { echo "release: push $COMMIT first" >&2; exit 2; }
  [ -f "docs/release-notes/$VERSION.md" ] || [ -f "docs/release-notes/${VERSION%-rc.*}.md" ] || { echo "release: write docs/release-notes/${VERSION%-rc.*}.md" >&2; exit 2; }
fi
[ -n "${SKIP_CHECK:-}" ] || make rewrite-check >/dev/null

rm -rf "$DIST" && mkdir -p "$DIST/context"

# A Docker config of our own: no desktop credential helper, which can hang
# waiting for the keychain; buildx comes from the user's plugins.
export BUILDX_CONFIG="${BUILDX_CONFIG:-$HOME/.docker/buildx}" # builders persist across runs
export DOCKER_CONFIG="$(mktemp -d)"
trap 'rm -rf "$DOCKER_CONFIG"' EXIT
echo '{}' > "$DOCKER_CONFIG/config.json"
[ -d "$HOME/.docker/cli-plugins" ] && ln -s "$HOME/.docker/cli-plugins" "$DOCKER_CONFIG/cli-plugins"
docker buildx inspect "$BUILDER" >/dev/null 2>&1 || docker buildx create --name "$BUILDER" --driver docker-container >/dev/null

echo "== binaries for the image"
for arch in amd64 arm64; do
  for p in oapd oap-ext-builder oap-ext-registry oap-ext-github; do
    CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" -o "$DIST/context/$p-linux-$arch" "./cmd/$p"
  done
done
cp cmd/oapd/Dockerfile "$DIST/context/"

echo "== oapd image"
LABELS="--label org.opencontainers.image.source=https://github.com/mohsalsaleem/OpenAppPlatform --label org.opencontainers.image.version=$VERSION --label org.opencontainers.image.revision=$COMMIT"
if [ -n "$DRY_RUN" ]; then
  docker buildx build --builder "$BUILDER" --platform linux/amd64,linux/arm64 $LABELS -t "$IMAGE_REPO:$VERSION" \
    --output "type=oci,dest=$DIST/oapd-image.oci.tar" "$DIST/context" >/dev/null 2>&1
  DIGEST=$(tar -xOf "$DIST/oapd-image.oci.tar" index.json | python3 -c 'import json,sys; print(json.load(sys.stdin)["manifests"][0]["digest"])')
else
  [ -n "$LOCAL_REGISTRY" ] || gh auth token | docker login ghcr.io -u "$(gh api user --jq .login)" --password-stdin >/dev/null
  docker buildx build --builder "$BUILDER" --platform linux/amd64,linux/arm64 $LABELS -t "$IMAGE_REPO:$VERSION" \
    --output "type=image,push=true" --metadata-file "$DIST/build.json" "$DIST/context" >/dev/null 2>&1
  DIGEST=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["containerimage.digest"])' "$DIST/build.json")
  rm -f "$DIST/build.json"
fi
PIN="$IMAGE_REPO:$VERSION@$DIGEST"
echo "$PIN" > "$DIST/oapd-image.txt"
echo "   $PIN"

echo "== oapctl"
for os in darwin linux; do
  for arch in amd64 arm64; do
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
      -ldflags "-s -w -X main.Version=$VERSION -X main.OapdImage=$PIN" -o "$DIST/oapctl-$os-$arch" ./cmd/oapctl
  done
done
rm -rf "$DIST/context"

(cd "$DIST" && shasum -a 256 oapctl-* oapd-image.txt > SHA256SUMS)
cat "$DIST/SHA256SUMS"

if [ -n "$DRY_RUN" ] || [ -n "$LOCAL_REGISTRY" ]; then
  echo "== no GitHub release ($DIST)"
  exit 0
fi
echo "== draft GitHub release"
PRE=""
case "$VERSION" in *-rc.*) PRE="--prerelease" ;; esac
NOTES="docs/release-notes/$VERSION.md"
[ -f "$NOTES" ] || NOTES="docs/release-notes/${VERSION%-rc.*}.md"
gh release create "$VERSION" --draft $PRE --target "$COMMIT" --title "OAP $VERSION" --notes-file "$NOTES" \
  "$DIST"/oapctl-* "$DIST/oapd-image.txt" "$DIST/SHA256SUMS"
echo "Draft release $VERSION created; publish it on GitHub when ready."
