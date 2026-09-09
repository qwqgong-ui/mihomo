# TCP Concurrent Winner Cache

[返回功能目录](../features.md) · [配置示例](../config.yaml)

缓存 `tcp-concurrent` 最近成功连接的至多两个地址，按连接耗时排序；确认地址仍属于当前 DNS 结果后同时尝试，并在失败或超时后回退正常并发连接。默认最多 4096 个目的地、有效期 30 分钟，渐进式 DIRECT 拨号也使用此缓存。命中地址的连接耗时按 RTT 采样,用于自适应下一次 fast-path 超时；TCP 并发拨号不使用 TFO，包括缓存命中、单一候选和渐进式 DIRECT 路径，以获得真实的连接结果。手动 flush DNS 缓存会一并清空该缓存。

Patches:

- `component/dialer.patch`
- `hub/route.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
