# Patched mihomo build contract

- This checkout is the downstream patched mihomo fork, not a vanilla MetaCubeX build.
- The tracked source tree intentionally stays close to upstream. The actual downstream source patches live in `patches/mihomo/*.patch` and must be applied in lexical order before compiling.
- Use `scripts/build-patched-linux-amd64.sh` for local Linux amd64 release builds. Do not use plain `go build` or `make linux-amd64` and call the result patched.
- A valid patched build must apply the complete patch chain, use the build tags `no_tailscale no_zerotier no_wireguard no_openvpn no_mieru no_sudoku` (and omit `with_gvisor`), and embed a version containing `patched-p<patch-digest>`.
- Keep the version marker and patch digest in artifact names so a binary can be distinguished from upstream without relying on chat memory.
- Before reporting success, verify `bin/mihomo-linux-amd64 -v`, the gzip archive, and their SHA-256 checksums.

