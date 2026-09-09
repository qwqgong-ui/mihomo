# Larger DNS Cache and Softer Optimistic TTL

[返回功能目录](../features.md) · [配置示例](../config.yaml)

`newCache` 的默认 `CacheMaxSize` 由 4096 提到 32768（`dns.cache-max-size` 仍可覆盖）。
乐观缓存命中已过期条目时返给客户端的 TTL 由 1s 提到 3s —— 后台 `continueFetch` 照常刷新，
但客户端不再几乎立刻重查。代价是 IP 刚变更的域名，每个客户端最多多用 3s 旧地址。

Patches:

- `dns.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
