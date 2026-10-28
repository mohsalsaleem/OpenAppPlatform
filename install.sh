#!/bin/sh
# Installs oapctl, OAP's command-line tool, from a GitHub release (R5.7).
#
#   curl -fsSL https://raw.githubusercontent.com/mohsalsaleem/OpenAppPlatform/main/install.sh | sh
#
# It downloads oapctl for this machine and the release's SHA256SUMS, and
# installs oapctl only if its checksum matches. It never uses sudo.
#   OAP_VERSION      a release tag (default: the latest release)
#   OAP_INSTALL_DIR  where to put oapctl (default: ~/.local/bin)
#   OAP_BASE_URL     where the release's files are (default: GitHub)
set -eu

repo="mohsalsaleem/OpenAppPlatform"
fail() { echo "install: $*" >&2; exit 1; }
command -v curl >/dev/null || fail "needs curl"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "oapctl runs on macOS and Linux, not $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "no oapctl for $(uname -m)" ;;
esac

version="${OAP_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$version" ] || fail "couldn't find the latest release; set OAP_VERSION"
fi
base="${OAP_BASE_URL:-https://github.com/$repo/releases/download/$version}"
dir="${OAP_INSTALL_DIR:-$HOME/.local/bin}"
file="oapctl-$os-$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading oapctl $version for $os/$arch"
curl -fsSL "$base/$file" -o "$tmp/$file" || fail "couldn't download $base/$file"
curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" || fail "couldn't download $base/SHA256SUMS"

want=$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || fail "SHA256SUMS has no entry for $file"
if command -v sha256sum >/dev/null; then
  got=$(sha256sum "$tmp/$file" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "$file doesn't match SHA256SUMS (got $got, want $want); not installed"

mkdir -p "$dir"
chmod 755 "$tmp/$file"
mv "$tmp/$file" "$dir/oapctl"
echo "Installed oapctl $version to $dir/oapctl (sha256 $got)"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
echo "Next: run oapctl, and have your server's IP address, username and password ready."
