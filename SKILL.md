# Mihomo Downstream Build Skill

This repository uses `Alpha` as the downstream branch and stores required downstream changes under `patches/`.

## Canonical rule

Never build or test the raw upstream-compatible source tree directly.

Before any build, test, benchmark, package, or release task:

1. Apply the complete `patches/mihomo/series` in its listed order with
   `sh patches/apply-mihomo-patches.sh`.
2. Apply dependency patches with `patches/apply-dependency-patches.sh`.
3. Use the downstream trimmed build tags:
   `no_tailscale no_zerotier no_wireguard no_openvpn no_mieru no_sudoku no_fake_tcp`.
4. Preserve the configured Go 1.27 (official release) / optimized build settings used by `.github/workflows/build.yml`.

## Forbidden shortcuts

Do not:

- run a default upstream `go build` and treat the result as a downstream artifact;
- use `with_gvisor` for downstream release builds;
- apply only a subset of `patches/mihomo/series`;
- omit `patches/apply-dependency-patches.sh` when building artifacts;
- copy upstream build matrices or packaging logic back into this repository;
- silently change the downstream build-tag set;
- package a newly rebuilt default binary instead of the already patched/trimmed binary.

If a patch no longer applies, stop and repair/rebase the patch chain. Do not bypass the failing patch to make the build pass.

## Current release targets

`.github/workflows/build.yml` intentionally builds only:

- Linux amd64, `GOAMD64=v3`;
- Windows amd64, `GOAMD64=v3`;
- Android arm64-v8.

Do not restore unrelated upstream architectures unless explicitly requested.

CMFA update automation is intentionally disabled and must not be restored during upstream synchronization.

## CI testing

`go test` runs in the `test` job of `.github/workflows/build.yml`, on the patched
tree and with the downstream build tags, so that tests carried by `patches/`
actually execute. Upstream's `.github/workflows/test.yml` tested the unpatched
tree with upstream build tags; it is intentionally removed and
`sync-upstream.yml` deletes it again on every upstream synchronization. Do not
restore it, and do not add a test job that skips the patch chain.
