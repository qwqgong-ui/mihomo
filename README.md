# Mihomo dev

`dev` 分支保留的下游功能、配置方式和限制统一记录在 docs：

- [分支功能目录](docs/features.md)：每个自定义功能独立成文，涵盖DIRECT 竞速与缓存、Fake-IP、DNS、AndroidCyaml 集成、协议和依赖修改。
- [Hybrid QUIC](docs/hybrid-quic.md)：通用代理流与 raw UDP、服务端要求及回退机制。
- [服务端域名 DNS bundle](docs/domain-dns-bundle.md)：节点级地址与 HTTPS 预热、缓存和兼容性。
- [配置示例](docs/config.yaml)。

Mihomo 自身修改已合入源码；外部依赖补丁仍由 `patches/apply-dependency-patches.sh` 应用。
