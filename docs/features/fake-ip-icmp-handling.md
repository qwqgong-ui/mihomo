# Fake-IP ICMP Handling

[返回功能目录](../features.md) · [配置示例](../config.yaml)

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

依赖侧的 ICMP 能力见 [sing-tun ICMP Ping](sing-tun-icmp-ping.md)。

Patches:

- `adapter/outbound.patch`
- `component/dialer.patch`
- `component/directrace.patch`
- `listener/sing_tun.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
