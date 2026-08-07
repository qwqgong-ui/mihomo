#!/usr/bin/env bash
set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly SOURCE_REF="${1:-HEAD}"
readonly TOOLCHAIN_ROOT="$(dirname "${REPO_ROOT}")/.local/mihomo-toolchains/go1.27"
readonly GO_BIN="${MIHOMO_GO_BIN:-${TOOLCHAIN_ROOT}/bin/go}"
readonly BUILD_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/mihomo-patched-build.XXXXXXXX")"
readonly SOURCE_DIR="${BUILD_ROOT}/src"

cleanup() {
    git -C "${REPO_ROOT}" worktree remove --force "${SOURCE_DIR}" >/dev/null 2>&1 || true
    case "${BUILD_ROOT}" in
        "${TMPDIR:-/tmp}"/mihomo-patched-build.*) rm -rf -- "${BUILD_ROOT}" ;;
    esac
}
trap cleanup EXIT

[[ -x "${GO_BIN}" ]] || {
    echo "Go 1.27 patched toolchain not found: ${GO_BIN}" >&2
    exit 1
}

mapfile -t PATCHES < <(find "${REPO_ROOT}/patches/mihomo" -maxdepth 1 -type f -name '*.patch' -print | sort)
(( ${#PATCHES[@]} > 0 )) || {
    echo "No mihomo patches found" >&2
    exit 1
}

readonly PATCH_DIGEST="$({
    for patch in "${PATCHES[@]}"; do
        sha256sum "${patch}"
    done
} | awk '{print $1}' | sha256sum | awk '{print substr($1, 1, 12)}')"

git -C "${REPO_ROOT}" worktree add --detach "${SOURCE_DIR}" "${SOURCE_REF}" >/dev/null

for patch in "${PATCHES[@]}"; do
    echo "Applying $(basename "${patch}")"
    git -C "${SOURCE_DIR}" apply --check --whitespace=error-all "${patch}"
    git -C "${SOURCE_DIR}" apply --whitespace=error-all "${patch}"
done
git -C "${SOURCE_DIR}" diff --check

readonly SOURCE_COMMIT="$(git -C "${SOURCE_DIR}" rev-parse --short=8 HEAD)"
readonly VERSION="alpha-${SOURCE_COMMIT}-patched-p${PATCH_DIGEST}"
readonly BUILD_TIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
readonly OUTPUT_DIR="${REPO_ROOT}/bin"
readonly OUTPUT="${OUTPUT_DIR}/mihomo-linux-amd64"
readonly ARCHIVE="${OUTPUT_DIR}/mihomo-linux-amd64-${VERSION}.gz"
readonly TEMP_BINARY="${BUILD_ROOT}/mihomo-linux-amd64"

mkdir -p "${OUTPUT_DIR}"
(
    cd "${SOURCE_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v3 "${GO_BIN}" build \
        -tags 'no_tailscale no_zerotier no_wireguard no_openvpn no_mieru no_sudoku' \
        -trimpath \
        -ldflags "-X github.com/metacubex/mihomo/constant.Version=${VERSION} -X github.com/metacubex/mihomo/constant.BuildTime=${BUILD_TIME} -w -s -buildid=" \
        -o "${TEMP_BINARY}" .
)

install -m 0755 "${TEMP_BINARY}" "${OUTPUT}"
gzip -9 -c "${OUTPUT}" > "${ARCHIVE}"
sha256sum "${OUTPUT}" "${ARCHIVE}" > "${OUTPUT_DIR}/mihomo-linux-amd64-${VERSION}.sha256"

echo
"${OUTPUT}" -v
echo "Patch digest: ${PATCH_DIGEST}"
echo "Binary: ${OUTPUT}"
echo "Archive: ${ARCHIVE}"
echo "Checksums: ${OUTPUT_DIR}/mihomo-linux-amd64-${VERSION}.sha256"
