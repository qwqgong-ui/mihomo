# Direct UDP ICMP Errors

[返回功能目录](../features.md) · [配置示例](../config.yaml)

直连 UDP 的数据报送不出去时，发送方原本什么都得不到：它面对的是一个收下了包的
tun 设备，于是继续按同样大小发，或者等满一个本可以由差错立即结束的超时。

两件事一起做才有意义。其一，**把发送方的 Don't Fragment 意图带到出站 socket**：
mihomo 用自己的 socket 重发载荷，发送方的 IP 头连同 DF 位一起被丢掉，于是做 PMTU
探测的应用（首当其冲是 QUIC）超长的探测包被本机内核静默分片、当成成功，最后收敛
到一个只有分片才能通过的尺寸 —— 而分片在路上被任何中间设备丢掉都是合法的。实测
一个 1600 字节数据报穿隧道发往直连目标，在物理网卡上是两个 IPv4 分片出去的，发送
方收不到任何提示。现在按发送方的原意设置：v4 跟随 DF 位，v6 无条件（IPv6 中间节点
本就不允许分片，代行路径职责的栈也不该分片）。没设 DF 的发送方行为不变。

其二，**写失败时合成 ICMP 差错回给发送方**：EMSGSIZE 回 Packet Too Big 并带上内核
知道的 MTU（连一个用同样选项、因而同样路由的临时 socket 读 `IP_MTU`/`IPV6_MTU`
拿到，不打扰承载流量的那个 socket，按目的地址缓存 5 秒），ECONNREFUSED、
EHOSTUNREACH、ENETUNREACH 分别回端口/主机/网络不可达。差错由 tun 栈合成，源地址是
发送方自己路由指向的下一跳（也就是这个栈），内嵌 RFC 792 要求的原始 IP 头加 UDP 头。
两侧地址族不同时（Fake-IP 落到另一族的真实地址）MTU 按头部大小差换算，低于 576/1280
则不报。

实测 v4：1600 字节 DF 数据报，第一次发出后发送方即学到 PMTU，第二次起本地
`Message too long`，`IP_MTU` 依次收敛到 1500、1480（1480 是这条线路的真实值）。v6：
差错同样送达并让发送方的下一次 `send()` 拿到 EMSGSIZE，但 Linux 不为其建立持久的
路由例外，因此发送方每次超长都要重新被告知一次 —— 对按失败探测调整尺寸的 QUIC 来说
够用，对依赖路由缓存的应用则是一次性信号。

Patches:

- `adapter/outbound.patch`
- `common/sockopt.patch`
- `component/dialer.patch`
- `constant.patch`
- `listener/sing.patch`
- `tunnel.patch`
- `../../sing-tun/0010-*.patch`、`0011-*.patch`（依赖补丁）

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
