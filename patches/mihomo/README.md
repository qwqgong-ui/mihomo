# Mihomo 下游补丁功能索引

补丁按每个 diff 修改的源码目录分组。`series` 是唯一应用顺序；本文档按下游功能反向索引同一补丁集。

## DNS Upstream Hostname IPv6 Preference

直连 DNS 上游地址为主机名时优先尝试 IPv6；IPv6 不可用或慢于 IPv4 时回退正常拨号路径。

Patches:

- `docs.patch`
- `tunnel.patch`

## Linux Endpoint-Aware Process Attribution

在 Linux 上使用本地和远端 socket endpoint 做精确进程归属查询，并缓存近期成功 PID 以减少 `/proc` 扫描。

Patches:

- `component/process.patch`
- `tunnel.patch`

## Remote UDP Destination Identity

对支持远端域名解析的 UDP 代理保留 FQDN，同时维持 DIRECT 的本地解析语义、精确 IP NAT 映射和必要的域名回复恢复。

Patches:

- `adapter/outbound.patch`
- `constant.patch`
- `tunnel.patch`

## Optional Protocol Build Tags

为 WireGuard、OpenVPN、Mieru 和 Sudoku 增加可选构建标签及 parser-compatible stub，并保留共享 IP stack 所需实现。

Patches:

- `adapter/outbound.patch`
- `common/convert.patch`
- `component/generator.patch`
- `constant/features.patch`
- `listener/inbound.patch`
- `listener/mieru.patch`
- `listener/sudoku.patch`

## DIRECT Dual-Stack Race

重叠执行 DIRECT 的 A/AAAA 查询与 IPv4/IPv6 TCP、UDP 和 ICMP 竞争，并接入近期 winner、resolver 超时和运行时 IPv6 能力门控。

Patches:

- `adapter/outbound.patch`
- `component/dialer.patch`
- `component/directrace.patch`
- `component/resolver.patch`
- `config.patch`
- `dns.patch`
- `listener/sing_tun.patch`
- `tunnel.patch`

## Fake-IP ICMP Handling

让 Fake-IP ICMP 请求经过正常规则判断，仅在最终 DIRECT 路径竞争真实目标，并将回复地址重写回原 Fake-IP。

Patches:

- `adapter/outbound.patch`
- `component/dialer.patch`
- `component/directrace.patch`
- `listener/sing_tun.patch`
- `tunnel.patch`

## Runtime IPv6 Availability Handling

检测可用的物理 IPv6，排除 Mihomo TUN、ULA、链路本地、Teredo、6to4 和已知隧道接口；不可用时同步关闭运行时 IPv6 路径。

Patches:

- `config.patch`

## Fake-IP HTTPS/SVCB Hint Synthesis

将 HTTPS/SVCB ServiceMode 地址提示改写为对应 Fake-IP，并同步维护 mandatory、RRSIG 和 AD 标志。

Patches:

- `dns.patch`

## Built-in Fake-IP Service Record Resolver

Fake-IP 模式下仅 SVCB/HTTPS（TYPE64/65）使用内置 `https://1.1.1.1/dns-query`，且 DoH 连接按 `RULES` 走代理。A/AAAA 仍由 fake-IP 池本地合成，主 `nameserver`、`direct-nameserver` 和 `proxy-server-nameserver` 语义不变。

Patches:

- `dns.patch`
- `dns/fakeip-service.patch`

## Process-Rule Candidate FD Filtering

从 PROCESS、UID、logic、rule-provider 和 wrapper 规则构造候选 matcher，在 Linux 扫描文件描述符前过滤不可能命中的进程。

Patches:

- `component/process.patch`
- `rules/common.patch`
- `rules/logic.patch`
- `rules/provider.patch`
- `rules/wrapper.patch`
- `tunnel.patch`

## Physical-Network DNS Dialing

发现系统的物理网络 DNS，绑定对应接口，并使用 sing-tun auto-redirect output mark 绕过 TUN。

Patches:

- `component/dialer.patch`
- `dns.patch`
- `docs.patch`
- `listener/sing_tun.patch`
- `tunnel.patch`

## TCP Concurrent Winner Cache

缓存 `tcp-concurrent` 最近成功连接的地址；确认它仍属于当前 DNS 结果后优先尝试，并在失败或超时后回退正常并发连接。

Patches:

- `component/dialer.patch`

## Provider Health-Check Probe Pacing

在 provider 之间共享健康检查启动节拍，以随机短间隔错开 probe，同时保留既有并发上限。

Patches:

- `adapter/provider.patch`

## HY2 ECN-Aware BBR

让 Hysteria2 客户端和服务端的既有 `bbr` 直接使用统一的 ECN-aware 状态机：validated ACK_ECN 零 CE 时快速扩张，首次 CE 冻结，持续 CE 按比例与 EWMA 渐进回缩，并在 ECN 不可用时回退到 delivery-rate、RTT 与 loss 控制。路径迁移会重新验证 ECN 并衰减路径相关模型。

Patches:

- `transport/tuic.patch`
- `../../quic-go/0001-hy2-expose-validated-ack-ecn-deltas.patch`（依赖补丁）
