# sing-tun System Stack

[返回功能目录](../features.md) · [配置示例](../config.yaml)

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
