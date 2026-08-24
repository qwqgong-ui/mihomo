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

配置 `ipv6: true` 时，Linux、Windows 与 macOS 监听接口和路由变化，事件静默 500ms 后复检物理网络 IPv6；不再是仅在配置解析时单向关闭。检测排除 Mihomo TUN、ULA、链路本地、Teredo、6to4 和已知隧道接口，配置解析与运行时监控共用同一份检测实现（`config.SystemIPv6Available`）。检测到可用时自动恢复运行时 IPv6，检测到失效时自动关闭，并保留 DNS、Fake-IP 与 TUN 的原始 IPv6 配置供之后恢复，无需重新加载配置。

状态切换通过 `hub/executor` mux 与 `resolver.DisableIPv6`（`atomic.Bool`）原子完成，重建对应 DNS 路径，并对 Mihomo 自建 TUN 增删 IPv6 配置。Android 核心不启动网络监听，由宿主的网络切换流程负责重新加载；外部传入文件描述符的 TUN（Android 等）不会被重启，以免关闭宿主 VPN 会话。REST API 的 `PATCH /configs` 的 `ipv6`/`tun` 字段现在都经过该控制器，而不是直接改写 resolver 或监听器。

Patches:

- `component/dialer.patch`
- `component/resolver.patch`
- `config.patch`
- `hub/executor.patch`
- `hub/route.patch`

## Fake-IP HTTPS/SVCB Hint Synthesis

将 HTTPS/SVCB ServiceMode 地址提示改写为对应 Fake-IP，并同步维护 mandatory、RRSIG 和 AD 标志。

Patches:

- `dns.patch`

## Built-in Fake-IP Service Record Resolver

Fake-IP 模式下仅 SVCB/HTTPS（TYPE64/65）使用内置 `https://1.1.1.1/dns-query`，且 DoH 连接按 `RULES` 走代理。A/AAAA 仍由 fake-IP 池本地合成，主 `nameserver`、`direct-nameserver` 和 `proxy-server-nameserver` 语义不变。

Patches:

- `dns.patch`
- `hub/executor.patch`

## Fake-IP FQDN Routing

Fake-IP 流量在规则匹配时不再向主 nameserver 查询真实地址；DIRECT 在最终出站阶段自行解析，其他代理出站则保留原始 FQDN。

Patches:

- `tunnel.patch`

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

缓存 `tcp-concurrent` 最近成功连接的地址；确认它仍属于当前 DNS 结果后优先尝试，并在失败或超时后回退正常并发连接。命中地址的连接耗时按 RTT 采样,用于自适应下一次 fast-path 超时；竞争多个候选时强制关闭 TFO 以获得真实的连接结果，单一候选时保留 TFO。手动 flush DNS 缓存会一并清空该缓存。

Patches:

- `component/dialer.patch`
- `hub/route.patch`

## Provider Health-Check Probe Pacing

在 provider 之间共享健康检查启动节拍，以随机短间隔错开 probe，同时保留既有并发上限。

Patches:

- `adapter/provider.patch`

## HY2 ECN-Aware BBR

让 Hysteria2 客户端和服务端的既有 `bbr` 直接使用统一的 ECN-aware 状态机：validated ACK_ECN 零 CE 时快速扩张，首次 CE 冻结，持续 CE 按比例与 EWMA 渐进回缩，并在 ECN 不可用时回退到 delivery-rate、RTT 与 loss 控制。路径迁移会重新验证 ECN 并衰减路径相关模型。

Patches:

- `transport/tuic.patch`
- `../../quic-go/0001-hy2-expose-validated-ack-ecn-deltas.patch`（依赖补丁）

## Reject-Rule Short-Circuit

命中 REJECT 或 REJECT-DROP 的连接在规则匹配后直接短路，不再拨出站、不再包装 deadline conn、traffic tracker 和双向 relay。TCP REJECT 立即关闭客户端连接；TCP REJECT-DROP 交给共享 parker 挂起，由单个按截止时间排序的 goroutine 统一释放，队列有上限以免洪水堆积 fd；UDP 在 nat 表中吸收整个会话，后续报文由已关闭的 sender 直接丢弃，不再重复匹配规则、拨号和打日志。`reject` 出站自身的 dropConn 同时成为真正的黑洞：写入被吞掉而不是报错。

Patches:

- `adapter/outbound.patch`
- `tunnel.patch`

