# DIRECT Dual-Stack Race

[返回功能目录](../features.md) · [配置示例](../config.yaml)

重叠执行 DIRECT 的 A/AAAA 查询与 IPv4/IPv6 TCP、UDP 竞争，并接入近期 winner、resolver 超时和运行时 IPv6 能力门控。
Fake-IP ICMP 按目标 Fake-IP 的地址族查询 A 或 AAAA，仅在同族真实地址之间竞争，不进行 IPv4/IPv6 跨族竞争或回退。

Patches:

- `adapter/outbound.patch`
- `component/dialer.patch`
- `component/directrace.patch`
- `component/resolver.patch`
- `config.patch`
- `dns.patch`
- `listener/sing_tun.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。

相关功能：[渐进解析与网络缓存](direct-nameserver-progressive-cache.md)、[DIRECT QUIC 胜出路径确认](direct-quic-winner-confirmation.md)。
