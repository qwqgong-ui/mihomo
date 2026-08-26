# Mihomo dev 下游修改索引

本文档记录 `dev` 分支相对上游 `Alpha` 保留的定制功能。Mihomo 补丁已在
2026-08-26 完整展开到 `dev` 源码，不再需要构建前应用 `patches/mihomo/series`。
各节中的 `Patches` 列表仅保留为迁移前的源码分组索引，实际实现以
`dev` 分支中的 Go 源码和测试为准。quic-go、sing-quic 和 sing-tun 仍是外部
Go module，因此它们的补丁继续保存在 `patches/` 下并由构建流程应用。

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
WireGuard 实现只在未使用 `no_wireguard` 时编译；裁剪构建使用同名 stub 保留配置解析类型，
但会在实例化时返回明确的功能已禁用错误。WireGuard 协议实现本身完整保留在
`wireguard.go`。`ipstack.go` 是 MASQUE、OpenVPN、WireGuard 和 ZeroTier 共用的网络栈抽象，
因此不能放进受 `!no_wireguard` 控制的文件；只有 gVisor 构造器再按 `with_gvisor` 分文件编译。

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

## Direct-Nameserver Auto ECS

`direct-nameserver` 的查询默认携带 EDNS Client Subnet，无需配置。子网来自对直连出口公网地址的探测：STUN 与 DNS whoami 查询（OpenDNS `myip.opendns.com`、Google `o-o.myaddr.l.google.com` TXT、Akamai `whoami.akamai.net`，均为固定 IP 上的 UDP/53）并发进行，谁先给出可用地址就用谁 —— 出口过滤 UDP/3478 时 STUN 会失败，whoami 仍可用。

IPv4 与 IPv6 独立探测独立保存，A 查询带 IPv4 子网、AAAA 带 IPv6 子网，只探到一个族时回退到该族；按 IPv4 /24、IPv6 /56 上报。探测在启动/重载和默认网卡变化（TUN 网络监听）时进行，整轮失败按 5s/15s/30s/60s 重试；STUN 主机名经 bootstrap（`default-nameserver`）解析，因而不依赖 `nameserver` 的配置方式，也不会与等待结果的 direct nameserver 相互死锁。

写了 `#ecs=` 参数的 direct nameserver 使用手动值；不附加 `ecs-override`，客户端自带的 ECS 不会被覆盖。其他 nameserver 列表不受影响。

Patches:

- `component/ecs.patch`
- `component/resolver.patch`
- `config.patch`
- `dns.patch`
- `docs.patch`
- `hub/executor.patch`
- `listener/sing_tun.patch`

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

## HY2 QUIC v2

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

## Reject-Rule Short-Circuit

命中 REJECT 或 REJECT-DROP 的连接在规则匹配后直接短路，不再拨出站、不再包装 deadline conn、traffic tracker 和双向 relay。TCP REJECT 立即关闭客户端连接；TCP REJECT-DROP 交给共享 parker 挂起，由单个按截止时间排序的 goroutine 统一释放，队列有上限以免洪水堆积 fd；UDP 在 nat 表中吸收整个会话，后续报文由已关闭的 sender 直接丢弃，不再重复匹配规则、拨号和打日志。`reject` 出站自身的 dropConn 同时成为真正的黑洞：写入被吞掉而不是报错。

Patches:

- `adapter/outbound.patch`
- `tunnel.patch`


## 3s Default DNS Timeout

`resolver.DefaultDNSTimeout` 由 5s 降到 3s。它是 `dns/util.go` 里 `batchExchange` 的
`picker.WithTimeout` 预算，决定上游全部无响应时单次解析要挂多久。实测静默黑洞下失败耗时
5.02s → 3.02s；极弱网（700ms±300ms、25% 丢包）成功率与 5s 持平（10/11），而 2s 会掉到 5/11。

Patches:

- `component/resolver.patch`

## Larger DNS Cache and Softer Optimistic TTL

`newCache` 的默认 `CacheMaxSize` 由 4096 提到 32768（`dns.cache-max-size` 仍可覆盖）。
乐观缓存命中已过期条目时返给客户端的 TTL 由 1s 提到 3s —— 后台 `continueFetch` 照常刷新，
但客户端不再几乎立刻重查。代价是 IP 刚变更的域名，每个客户端最多多用 3s 旧地址。

Patches:

- `dns.patch`

## Persisted DNS Answer Cache

DNS 应答缓存写入 `cache.db`（新 `dnscache` bucket），每小时一次加关机时 flush，
启动时载回。每条显式带过期时间：ARC 读取时不检查 `expires`，不带的话恢复后无法区分新鲜与陈旧；
带上之后，停机期间过期的条目会以陈旧状态恢复，由既有的乐观缓存路径返回一次并后台刷新。

载入必须由 `hub/executor` 在 DNS 就绪之后调用 `dns.LoadPersistentCache()` 触发，
**不能在 resolver 构造期间碰 `cachefile.Cache()`** —— 那是个单例，会把首次看到的路径永久锁定，
而此时运行时还没把 `C.Path` 指向 `-d` 目录，结果整个进程改去打开
`$HOME/.config/mihomo/cache.db`；在 systemd 下 root 无 HOME，打开失败、`DB` 为 nil，
fake-ip 池连带失去存储，所有 fake IP 反查失败。

写入用 `DB.Update` 而非 `DB.Batch`：`Batch` 会延迟提交以合并调用者，而关机路径在那之前就退出了。

`arc` 和 `lru` 两种 `cache-algorithm` 都支持，各自加了 `Snapshot()`：ARC 跳过 ghost 条目，
LRU 按最近最少使用顺序返回以便恢复时重放同样的 recency。两者的 item 类型不同，
`dns/cachestore.go` 里做归一化。

main / proxy-server / direct 三个 resolver 的缓存共用一个 bucket，键前缀区分，避免碰撞。

Patches:

- `common/arc.patch`
- `common/lru.patch`
- `component/profile.patch`
- `dns.patch`
- `hub/executor.patch`

## sing-tun System Stack

只改依赖 `github.com/metacubex/sing-tun`，mihomo 自身源码不动，补丁在
`patches/sing-tun/`，由 `patches/apply-dependency-patches.sh` 应用。针对
`stack: system`（`mixed` 共用同一份 System 实现）的数据面。

- `0001` `batchLoopLinux` 的写回列表用 `make([][]byte, batchSize)` 分配，
  开 GSO 时 batchSize=128，第一批写回因此带 128 个 nil，`handleGRO` 判为
  `invalid offset` 而整批丢弃。改为零长度满容量。附带 darwin 批量读循环把
  `EBADF` 当正常关闭，`acceptLoop` 的每连接闭包提为方法。
- `0002` `LookupBack()` 是回程每包都走的路径，原来要 RWMutex 读锁 + map
  哈希 + session 互斥锁才更新一个时间戳。改为按 NAT 端口索引的原子指针数组
  （10000-65535，444 KiB）与原子秒级时间戳，回程变成一次原子读。端口分配
  同时修掉两个缺陷：计数器回绕后会发出仍在使用的端口；同一 flow 并发进入时
  各分配一个端口，先分配的那个永久留在表里。
- `0003` GRO 合并前对头包和来包各做一次全量 L4 校验和验证，是整个 payload
  的第二遍。写入 tun 的包都是本进程生成的（system 栈 NAT 改写后重算过，或
  packet writer 新建的头），加 `trustCSum` 跳过。顺带让 `_TXChecksumOffload`
  故意置零的校验和不再使合并全部失效。
- `0004` `processIPv4TCP`/`processIPv6TCP` 改写地址和端口后原本重算整段
  TCP 校验和（读侧 GRO 后单包可达 64 KiB）。改写只动两个地址和两个端口，
  伪头里的长度、协议和 payload 都没变，按 RFC 1624 eqn.3 增量更新。
  `_TXChecksumOffload` 是包私有字段、mihomo 从不设置，所以这条全量重算
  在所有平台上对每个包都在跑。含 4000 组随机 v4/v6 包与全量重算的对拍测试。
- `0005` 上游在 TCP flow 首包调 `PrepareConnection`，`ErrDrop` 丢包、其他
  错误回 RST；本 fork 只接了 ICMP，被拒的 TCP 要走完本地握手、accept 之后
  再关。补上该调用。mihomo 侧 `PrepareConnection` 对 TCP 仍返回 nil，行为
  不变，但拒绝能力就位了。UDP 未接：上游挂在自己的 udpnat2 上，本 fork 栈内
  没有 UDP 会话表，mihomo 的 NAT 在 tunnel 里，为找首包再建一张表得不偿失。

`Write()` 的写合并（把单包写入队、由持锁方统一 flush 以喂饱 GRO）试过并
**撤掉了**：实测同一 flow 的包由同一个 goroutine 顺序写出，永远不会同时待在
队列里，而并发的多个 goroutine 属于不同 flow，GRO 合并不了。20000 包实测
write(2) 次数比 1.000（不同 flow，1/8/64 writer）到 0.967（同 flow 64 writer），
而每包多出的一对 mutex 让 flows=1 慢 5%。真要拿到 UDP GSO，得让 mihomo 的
UDP 回写 API 一次交多个包，而不是在 tun 这层等。
