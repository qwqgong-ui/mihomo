# HY2 ECN-Aware BBR

[返回功能目录](../features.md) · [配置示例](../config.yaml)

让 Hysteria2 客户端和服务端的既有 `bbr` 直接使用统一的 ECN-aware 状态机：validated ACK_ECN 零 CE 时快速扩张，首次 CE 冻结，持续 CE 按比例与 EWMA 渐进回缩，并在 ECN 不可用时回退到 delivery-rate、RTT 与 loss 控制。路径迁移会重新验证 ECN 并衰减路径相关模型。

Patches:

- `transport/tuic.patch`
- `../../quic-go/0001-hy2-expose-validated-ack-ecn-deltas.patch`（依赖补丁）

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
