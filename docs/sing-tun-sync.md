# sing-tun synchronization

Mihomo currently imports `github.com/metacubex/sing-tun` together with the
MetaCubeX `sing` ecosystem. The original `SagerNet/sing-tun` development branch
now imports `github.com/sagernet/sing` v0.8.x, while Mihomo and its other sing
modules use `github.com/metacubex/sing` v0.5.x. Replacing only sing-tun would
therefore mix distinct Go type graphs and fail to compile.

`.github/workflows/sync-sing-tun.yml` follows both repositories:

- `SagerNet/sing-tun:dev` is recorded as the original upstream head.
- `MetaCubeX/sing-tun:meta` is used as the newest source-compatible head.

Every six hours, and on manual dispatch, the workflow resolves the compatible
branch to an exact commit, updates `go.mod` and `go.sum`, runs module integrity
checks, runs the complete test suite with and without gVisor, and pushes only
when every check succeeds. A concurrent branch update is never overwritten.
Dependabot ignores this one module so that the branch-tip synchronizer is its
single update owner.

Moving directly to `SagerNet/sing-tun:dev` requires migrating the rest of
Mihomo's sing-based dependencies and adapters to the SagerNet v0.8 type graph in
one coordinated change; it is not safe as an isolated module replacement.
