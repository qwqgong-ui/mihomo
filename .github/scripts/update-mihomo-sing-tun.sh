#!/usr/bin/env bash
set -Eeuo pipefail

: "${INTEGRATION_VERSION:?INTEGRATION_VERSION is required}"
: "${REPLACEMENT_REPOSITORY:?REPLACEMENT_REPOSITORY is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

# Keep Mihomo's import path unchanged. The replacement repository contains a
# snapshot whose go.mod still declares github.com/metacubex/sing-tun.
go mod edit -dropreplace=github.com/metacubex/sing-tun || true
go mod edit \
  -replace="github.com/metacubex/sing-tun=${REPLACEMENT_REPOSITORY}@${INTEGRATION_VERSION}"

GOPROXY=direct go mod tidy
go mod download
go mod verify
GOPROXY=direct go mod tidy -diff
go test ./... -count=1
go test ./... -count=1 -tags with_gvisor

if git diff --quiet -- go.mod go.sum; then
  echo "changed=false" >> "$GITHUB_OUTPUT"
else
  echo "changed=true" >> "$GITHUB_OUTPUT"
fi
