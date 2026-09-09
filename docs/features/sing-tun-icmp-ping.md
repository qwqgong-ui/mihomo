# sing-tun ICMP Ping

[返回功能目录](../features.md) · [配置示例](../config.yaml)

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
