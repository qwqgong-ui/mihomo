# HY2 QUIC v2

[返回功能目录](../features.md) · [配置示例](../config.yaml)

Hysteria2 出站的 `quic.Config.Versions` 固定为 `[v2]`，首包按 QUIC v2（RFC 9369）发出。无配置项。

**没有版本回落**：hysteria2 的认证走 HTTP/3，而 `http3.Transport` 在 `Versions` 多于一个时直接报
`can only use a single QUIC version for dialing a HTTP/3 connection`（quic-go `http3/transport.go:152`），
所以 quic-go 的 Version Negotiation 回落在这里用不了，只能填单个版本。服务端必须支持 v2，否则握手失败。

mihomo 自己的 hy2 入站和 Xray-core 的 hysteria 入站都不设 `Versions`，quic-go 会填成
`SupportedVersions`（v1 和 v2），因此两者都能接 v2。

v2 与 v1 的帧格式相同，只有版本号、Initial salt/HKDF label 和长包头 packet type 编码不同，
不影响 hy2 自身协议、Salamander/Gecko 混淆和端口跳跃。

Patches:

- `adapter/outbound.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
