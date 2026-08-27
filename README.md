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

途中路由器回的 ICMP 差错（超时、不可达、包太大）也一并转交，因此 Fake-IP 上的
traceroute/`ping -t` 是真的：外层源地址保留报告差错的那台路由器，只把差错内嵌的
原始数据报里的目的地址改回 Fake-IP，内层与外层校验和按 RFC 1624 eqn.3 增量更新
（路由器只保证回传被丢数据报的前若干字节，内层传输层校验和无法重算）。竞速期间
同一个探测发往全部候选，低 TTL 会每个候选各回一份，按 (id, seq) 只放行最先报告
的那台，应用看到的一跳就是一行。差错不设 winner，也不消费请求表条目。

Fake-IP 的域名解不出真实地址时（打错的名字、已死的域名、被直接喂给 ping 的
URL）回 ICMP 目标不可达，而不是本地合成 echo reply：真实流量在 DIRECT 出站解析
该名字时同样会失败，合成回复等于报告一个谁也连不上的主机是通的。所有候选都连不
上时同理。失败不入 direct route 表，下一个 echo request 会重新解析，瞬时故障自
愈。代理路由的 Fake-IP 和丢失 host 映射的 Fake-IP 仍走合成 echo reply。

traceroute 要用 ICMP 模式（`traceroute -I`，或默认走 ICMP 的 mtr）。默认的 UDP
模式全是星号：UDP 进的是 mihomo 的 NAT，客户端 TTL 没被带到出站 socket，回程的
ICMP 差错也没有注入回 TUN 的路径。`-T` 的 TCP 模式则会把目标显示在第一跳 —— TCP
在 TUN 里本地终结，TTL 在那里没有意义，这条不是能修的。

依赖侧的 ICMP 能力见下面的 `sing-tun ICMP Ping`。

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

## Fake-IP Host-Name Validation

Fake-IP 的 A/AAAA 不问上游，客户端查什么都给地址，包括没有任何域名服务器会解析的
字符串 —— 直接喂给 ping 或 curl 的 URL 就是这样。拿到的地址对所有协议都是死的：
DIRECT 在出站解析该名字时同样失败，用户看到的却像是路由问题，其实是个笔误。合成
之前先检查名字能不能是个主机名，不能就回 NXDOMAIN，把失败摆回它真正发生的地方，
也和不挂隧道时看到的结果一致（`名称或服务未知`）。

判据是字符而不是注册名：下划线（`_dmarc`、`_http._tcp`）、单标签（局域网主机名）
和 punycode 都是正当查询；而 miekg/dns 的呈现形式里出现转义序列，就意味着线格式
里带了主机名不可能有的字节。只作用于 Fake-IP 合成的 A/AAAA，其他类型不变。

Patches:

- `dns.patch`

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
- `0010` UDP 流的数据报送不出去时没有任何东西能告诉发送方，因为它面对的是一个
  收下了包的 tun 设备。栈正站在那台无法转发的路由器的位置上，也是唯一还留着差错
  必须引用的 IP 与 UDP 头的地方，所以给 handler 一个 `ReportICMPError`：Packet Too
  Big、端口/主机/网络不可达。源地址取发送方路由指向的下一跳（栈自己的 next address），
  该族没有地址时退回发送方本来要去的地址。只做 system 栈，gvisor 的写回走的是栈内
  路由而不是手搓包。
- `0011` handler 用自己的 socket 重发载荷，丢掉了发送方 IP 头里的 DF 位，于是做
  PMTU 探测的发送方（QUIC）超长探测被本机内核静默分片当成成功。把该位从引用头里
  报给 handler，让它在自己的 socket 上照办；IPv6 无条件，因为端点之间没有任何节点
  被允许分片。

- `0006` Go 1.24 起 listener 默认开 MPTCP，而 redirect server 从不显式设置，
  于是跟随默认值。MPTCP socket 上 `getsockopt(SOL_IP, SO_ORIGINAL_DST)` 返回
  `EOPNOTSUPP`，`loopIn()` 取不到目的地会把 MPTCP 客户端的每条重定向连接直接丢弃。
  显式关掉，不再依赖主模块 go 指令把 GODEBUG 压在旧值上。

`Write()` 的写合并（把单包写入队、由持锁方统一 flush 以喂饱 GRO）试过并
**撤掉了**：实测同一 flow 的包由同一个 goroutine 顺序写出，永远不会同时待在
队列里，而并发的多个 goroutine 属于不同 flow，GRO 合并不了。20000 包实测
write(2) 次数比 1.000（不同 flow，1/8/64 writer）到 0.967（同 flow 64 writer），
而每包多出的一对 mutex 让 flows=1 慢 5%。真要拿到 UDP GSO，得让 mihomo 的
UDP 回写 API 一次交多个包，而不是在 tun 这层等。

## sing-tun ICMP Ping

同样只改依赖，补丁在 `patches/sing-tun/`。上游 sing-tun 的 ping 包在本 fork 的
v0.4.x 血统里与 sagernet v0.8.13 逐字节相同；v0.9.0-beta.2 重写了 ICMP 这块，
`0007`/`0009` 是那次重写的移植，`0008` 是上游至今没做的 IPv6 部分。

- `0007` 三处让隧道内的 ICMP 诊断结果失真的缺陷。`WriteIP` 丢掉客户端的 IP 头
  只写 ICMP 报文，socket 用内核默认 TTL，于是 `ping -t 1` 和 traceroute 的每一跳
  都直达真实目标；现在 TTL/hop limit 跟着包走，用 `lastTTL`/`lastHopLimit` 缓存，
  稳定 ping 不会每包一次 setsockopt。socket 原本是 connected 的，而 connected 的
  ICMP socket 只收得到 peer 发来的报文，途中路由器的 Time Exceeded 根本到不了；
  Linux privileged 与 Windows 改用非连接 socket，随之而来的是 Go 的
  `IPConn.ReadFrom` 会剥掉 IPv4 头、只有 `ReadMsgIP` 保留，raw v4 读路径改用后者。
  `loopRead` 原本丢掉一切非 echo reply，现在 Time Exceeded 与 Destination
  Unreachable 按差错内嵌的数据报匹配未完成请求，就地还原 wire identifier 与本地
  源地址后转交，外层源地址保持为报告差错的路由器。
  非连接的 raw ICMP socket 会收到本机每个 ICMP 包的副本，因此附带一个 BPF 程序，
  只留本 flow wire identifier 的 echo reply 加上那两类差错；identifier 的异或映射
  同时意味着我们注入回隧道的包永远匹配不上自己的过滤器。一个 Conn 被第二个
  identifier 复用时过滤器直接摘掉而不是错误地收窄。
- `0008` 上游那次重写只做了 ICMPv4：BPF 已经放行 ICMPv6 的差错类型，但
  `loopRead` 的 v6 分支仍然只放 echo reply，IPv6 traceroute 依旧全是星号。按 v4
  的做法处理 Time Exceeded、Destination Unreachable 和 Packet Too Big；内嵌的是
  完整 IPv6 头加原 echo request，两层校验和都带伪头，因而按地址重算而非增量更新。
  `ReadIP` 原本对收到的每个 ICMPv6 报文都做 identifier 反映射，而差错报文的那两个
  字节根本不是 identifier —— 是未用字段，或者 Packet Too Big 的 MTU —— 现在只对
  echo reply 做。
- `0009` `loopRead` 的读截止时间原本从每次读开始算，于是客户端还在发包时
  destination 也会在最后一次**回包**之后一个 timeout 被拆掉，且截止时间到达是走
  错误路径报出来的：每个结束的 ping 会话都在日志里留一条 `receive ICMP echo
  reply: i/o timeout`。改为跟踪最后活动时间（写也算），截止时间到了就继续循环，
  空闲满一个 timeout 才退出。这条在本 fork 比上游更要紧：Fake-IP 竞速自己持有多个
  destination 且跨它们存活，某个候选在稳定 ping 下自行关闭就等于永久退出竞速。

## Go 1.26 Language Version

`go.mod` 的 go 指令从上游的 `go 1.20` 抬到 `go 1.26`。上游整套 fork
（sing、sing-tun、quic-go）都停在 1.20，所以这是一处会在每次 upstream sync
PR 里冲突的分歧，合并时保留下游的 go 指令。

抬升的动机是依赖：`golang.org/x/net`、`golang.org/x/text`、`golang.org/x/crypto`
和 `miekg/dns` 的当前版本都声明 `go 1.25.0`，主模块停在 1.20 就无法 require
它们，也就拿不到 x/net 的 HTTP/2 与 idna 安全修复。依赖升级见下节。

本改动只动语言版本，不改运行时行为：

- go 指令一抬，26 个原本被钉在 go1.20 默认值上的 GODEBUG 会全部释放。抬升时
  先用 `godebug` 块把它们钉回原值以隔离风险，之后整块移除，现在全部采用
  go1.26 默认值（见下节）。
- 唯一无法用 `godebug` 钉住的是 Go 1.22 的循环变量按迭代语义。用
  `-gcflags=github.com/metacubex/mihomo/...=-d=loopvar=2` 量化过：本仓库自身代码
  只有 4 个循环受影响，全部 stack-allocated（循环变量没有被闭包或 goroutine
  捕获），可观察差异为 0。依赖仍声明 go 1.20，不受影响。
- Go 1.24 起 `printf` 分析器会检查非常量格式串，`go test` 默认跑 vet，
  因此 25 处「把运行时字符串当格式串传」的调用必须先修，否则 CI 直接红。
  这些是真问题：消息里含 `%` 时会输出成 `%!x(MISSING)`。
## MPTCP Listener Guards

`listener/tproxy` 早就显式 `SetMultipathTCP(false)`，理由写在注释里：Go 1.24 起
listener 默认开 MPTCP，会让 tproxy 在某些内核上失效。同类的两处漏了，本分支补上：
`listener/redir`（裸 `net.Listen`）和 sing-tun 的 redirect server（补丁 `0006`）。

实测（Linux 7.2，`net.mptcp.enabled=1`）：

```
plain TCP   conn_mptcp=false  SO_ORIGINAL_DST -> 127.0.0.1:21077
MPTCP       conn_mptcp=true   SO_ORIGINAL_DST -> ERROR operation not supported
```

要开 inbound MPTCP 用配置项 `inbound-mptcp: true` —— 走
`adapter/inbound.ListenConfig` 的显式设置，对每个正常 inbound 生效，而
`net.ListenConfig` 的 MPTCP 是三态，显式值优先于 GODEBUG，所以
`multipathtcp=0` 钉着也照样能建立真 MPTCP 连接（实测 `conn_mptcp=true`）。
换句话说放开 `multipathtcp` 这个 GODEBUG 并不会让正常 inbound 多拿到什么，
只会波及这些不显式设置的监听器。

## GODEBUG Defaults Released

`go 1.26` 引入时加的 `godebug` 块（把 24 项钉回 go1.20 默认值）已整块移除，
现在全部采用 go1.26 默认值。与之前相比实际发生变化的项：

| setting | 之前（go1.20 默认） | 现在（go1.26 默认） | 影响面 |
|---|---|---|---|
| `multipathtcp` | 0（关） | 2（listener 默认开 MPTCP） | 只波及不显式设置的监听器；两个 redirect 监听器已在上一节的防护里显式关闭，正常 inbound 由 `inbound-mptcp` 显式控制 |
| `tlssha1` | 1（允许） | 0（拒绝 TLS 1.2 的 SHA-1 签名） | 标准库 TLS 与证书校验 |
| `rsa1024min` | 0（允许） | 1（拒绝 <1024 位 RSA） | 同上 |
| `x509negativeserial` | 1（容忍） | 0（拒绝负序列号证书） | 同上 |
| `x509rsacrt` / `x509sha256skid` / `x509usepolicies` | 旧值 | 新值 | 证书解析与策略校验 |
| `tlsmlkem` / `tlssecpmlkem` | 0 | 1（默认启用后量子密钥交换） | 标准库 TLS 的 ClientHello |
| `httplaxcontentlength` | 1（容忍畸形 Content-Length） | 0（拒绝） | HTTP 客户端与服务端 |
| `httpservecontentkeepheaders` / `httpmuxgo121` | 旧语义 | 新语义 | `net/http` |
| `httpcookiemaxnum` / `urlmaxqueryparams` / `urlstrictcolons` | 0（无限制/宽松） | 新限制生效 | Go 1.26 新增的解析上限 |
| `panicnil` | 1（`panic(nil)` 不转换） | 0（转成 `*runtime.PanicNilError`） | 运行时 |
| `containermaxprocs` / `updatemaxprocs` | 0 | 1（cgroup 感知 GOMAXPROCS） | 容器内自动调整 P 数 |
| `cryptocustomrand` / `decoratemappings` / `randseednop` / `winsymlink` / `winreadlinkvolume` / `gotestjsonbuildtext` | 旧值 | 新值 | 低风险 |

代理出站 TLS 大多走 `metacubex/utls`，不受 `tls*` 这几项影响；但证书校验仍是
标准库 `crypto/x509`，所以 `rsa1024min`、`x509negativeserial`、`tlssha1` 会
影响到用弱参数或畸形证书的对端。真遇到问题，单独把对应项加回 `godebug` 块
即可，不必回退整个改动。

## Dependency Security Upgrades

go 指令抬到 1.26 之后可以取到的更新。govulncheck 符号级扫描（`-mode=binary`）
从 **4 个可达漏洞降到 0**，模块层面的不可达项从 29 降到 1。

修掉的可达项：

| | 模块 | 修复版本 | 可达符号 |
|---|---|---|---|
| GO-2026-4918 | x/net | v0.53.0 | `http2.Transport.RoundTrip` —— 对端发畸形 `SETTINGS_MAX_FRAME_SIZE` 让 HTTP/2 客户端死循环 |
| GO-2026-5026 | x/net | v0.55.0 | `idna.ToASCII` —— Punycode 标签校验绕过 |
| GO-2025-3503 | x/net | v0.36.0 | `httpproxy.config.useProxy` —— IPv6 Zone ID 代理绕过 |
| GO-2026-5970 | x/text | v0.39.0 | `norm.Form.*` —— 畸形输入死循环 |

另外顺带修掉两个不可达项：`klauspost/compress` 的 s2 OOB read（GO-2026-5841）
和 `insomniacslk/dhcp` 的畸形 IPv4 包 DoS（GO-2026-6237）。

版本变动：

```
github.com/insomniacslk/dhcp  20250109 -> 20260728
github.com/klauspost/compress v1.17.9  -> v1.19.2
github.com/miekg/dns          v1.1.63  -> v1.1.73
golang.org/x/crypto           v0.33.0  -> v0.55.0
golang.org/x/net              v0.35.0  -> v0.58.0
golang.org/x/sync             v0.11.0  -> v0.22.0
golang.org/x/sys              v0.30.0  -> v0.47.0
golang.org/x/mod/term/text/tools                    (indirect，随之拉起)
```

剩下唯一一项是 GO-2026-5932（`x/crypto/openpgp` 已废弃，无修复版本），不可达。

## sing-mux h2mux

只改依赖 `github.com/metacubex/sing-mux`，补丁在 `patches/sing-mux/`。

x/net **v0.54.0** 起 `x/net/http2` 在 **Go 1.27 工具链**下会走 net/http 的请求
校验（`//go:build go1.27 && !http2legacy`），而 sing-mux 的 h2mux 客户端为每条
流构造的 `http.Request` 字面量没有 `Header` 字段，于是 `RoundTrip` 直接返回
`http: nil Request.Header`，每条流都开不起来 —— 调用方看到请求体上的 closed
pipe 和等响应头的超时。表现是
`TestInboundVless_Encryption/**/singmux/h2mux/Sequential` 96 条全部挂 60s。

`http.Request` 的 `Header` 本来就不允许为 nil，只是旧版 `x/net/http2` 没去看它。
补上一个空 map 即可，对新旧两种实现都成立。

排查过程中试过另外两条路，都比这个差：把 x/net 钉在 v0.53.0（最后一个可用版本）
需要 `replace` 压过 x/crypto、miekg/dns 和 x/text 声明的 v0.57.0，且拿不到
v0.55.0 的 idna 修复；用上游的 `http2legacy` 构建标签则要求每条构建命令都别忘了
带，漏了就静默坏掉。补 sing-mux 一行两者都不需要。
