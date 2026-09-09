# Direct-Nameserver Auto ECS

[返回功能目录](../features.md) · [配置示例](../config.yaml)

`direct-nameserver` 的查询默认携带 EDNS Client Subnet，无需配置。子网来自对直连出口公网地址的探测：STUN 与 DNS whoami 查询（OpenDNS `myip.opendns.com`、Google `o-o.myaddr.l.google.com` TXT、Akamai `whoami.akamai.net`，均为固定 IP 上的 UDP/53）并发进行，谁先给出可用地址就用谁 —— 出口过滤 UDP/3478 时 STUN 会失败，whoami 仍可用。

IPv4 与 IPv6 独立探测独立保存，A 查询带 IPv4 子网、AAAA 带 IPv6 子网，只探到一个族时回退到该族；按 IPv4 /24、IPv6 /56 上报。探测在启动/重载和默认网卡变化（TUN 网络监听）时进行，切网时立即清除旧网络的 ECS 子网并取消旧探测，旧探测结果不会写回；5 秒内的连续更新会合并延后执行，不丢弃最后一次更新。整轮失败按 5s/15s/30s/60s 重试；STUN 主机名经 bootstrap（`default-nameserver`）解析，因而不依赖 `nameserver` 的配置方式，也不会与等待结果的 direct nameserver 相互死锁。

写了 `#ecs=` 参数的 direct nameserver 使用手动值；不附加 `ecs-override`，客户端自带的 ECS 不会被覆盖。其他 nameserver 列表不受影响。

Patches:

- `component/ecs.patch`
- `component/resolver.patch`
- `config.patch`
- `dns.patch`
- `docs.patch`
- `hub/executor.patch`
- `listener/sing_tun.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
