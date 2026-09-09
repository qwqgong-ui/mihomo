# 3s Default DNS Timeout

[返回功能目录](../features.md) · [配置示例](../config.yaml)

`resolver.DefaultDNSTimeout` 由 5s 降到 3s。它是 `dns/util.go` 里 `batchExchange` 的
`picker.WithTimeout` 预算，决定上游全部无响应时单次解析要挂多久。实测静默黑洞下失败耗时
5.02s → 3.02s；极弱网（700ms±300ms、25% 丢包）成功率与 5s 持平（10/11），而 2s 会掉到 5/11。

Patches:

- `component/resolver.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
