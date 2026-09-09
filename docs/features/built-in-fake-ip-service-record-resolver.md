# Built-in Fake-IP Service Record Resolver

[返回功能目录](../features.md) · [配置示例](../config.yaml)

Fake-IP 模式按查询域名的 HTTPS/TCP 443 规则选择最终代理节点，再通过该节点的
流连接请求保留目的地 `server-dns.invalid:53`。保留名不再次匹配规则，也不做公网解析。
DNS 无法预知应用后续连接的端口、网络和来源；若实际连接选中不同节点，不能假定两端 DNS 数据相同。

首次合成 A/AAAA Fake-IP 前，先向节点预热真实地址与 HTTPS 记录组成的完整 bundle。
请求使用 DNS-over-TCP、单个 HTTPS 问题和实验性 EDNS 65001 扩展；支持的服务端
在 Answer 返回 HTTPS，在 Additional 返回真实地址，并确认扩展。Fake-IP 仍是本地域名索引，
并不直接将真实地址返回给应用。HTTPS/SVCB 的地址提示会改写为 Fake-IP，保留 ALPN、ECH 等服务参数。

bundle 按最终节点名称和域名隔离，使用完整结果的最短 TTL；到期不返回陈旧 bundle，
并发查询共享请求，配置重载后重新建立。预热失败仍可分配 Fake-IP，不触发公共 A/AAAA 解析。
不支持扩展的旧节点回退普通服务记录查询，扩展不支持状态记忆 5 分钟。
服务记录的节点查询失败后可顺序回退公共 DoH；这与地址预热的失败处理不同。

完整协议、缓存边界和匹配 Xray 的集成验证方法见
[服务端域名 DNS bundle](../domain-dns-bundle.md)。服务端配套行为需要相应 Xray 实现，不能只升级客户端。
