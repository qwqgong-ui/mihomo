# Remote UDP Destination Identity

[返回功能目录](../features.md) · [配置示例](../config.yaml)

对支持远端域名解析的 UDP 代理保留 FQDN，同时维持 DIRECT 的本地解析语义、精确 IP NAT 映射和必要的域名回复恢复。

Patches:

- `adapter/outbound.patch`
- `constant.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
