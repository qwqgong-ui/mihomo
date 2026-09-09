# Reject-Rule Short-Circuit

[返回功能目录](../features.md) · [配置示例](../config.yaml)

命中 REJECT 或 REJECT-DROP 的连接在规则匹配后直接短路，不再拨出站、不再包装 deadline conn、traffic tracker 和双向 relay。TCP REJECT 立即关闭客户端连接；TCP REJECT-DROP 交给共享 parker 挂起，由单个按截止时间排序的 goroutine 统一释放，队列有上限以免洪水堆积 fd；UDP 在 nat 表中吸收整个会话，后续报文由已关闭的 sender 直接丢弃，不再重复匹配规则、拨号和打日志。`reject` 出站自身的 dropConn 同时成为真正的黑洞：写入被吞掉而不是报错。

Patches:

- `adapter/outbound.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
