#!/usr/bin/env bash
# Install the pinned code-generation tools into .local/bin.
set -euo pipefail
cd "$(dirname "$0")/.."
. tools/versions.env
export GOBIN="$PWD/.local/bin"
go install "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}"
go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}"
go install "connectrpc.com/connect/cmd/protoc-gen-connect-go@${PROTOC_GEN_CONNECT_GO_VERSION}"
"$GOBIN/buf" --version
