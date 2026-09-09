# Fake-IP FQDN Routing

[返回功能目录](../features.md) · [配置示例](../config.yaml)

Fake-IP 流量在规则匹配时不再向主 nameserver 查询真实地址；DIRECT 在最终出站阶段自行解析，其他代理出站则保留原始 FQDN。

Patches:

- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
