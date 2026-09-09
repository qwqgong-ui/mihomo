# DIRECT QUIC 胜出路径确认

[返回功能目录](../features.md) · [配置示例](../config.yaml)

QUIC 收到首个响应不立即记为胜出地址；结合服务端 Connection ID 与后续客户端发包确认应用选择的路径，
再记录供复用。应用一直没有确认时，竞速也受预算限制，避免无限复制数据报。
普通 UDP 与 QUIC 的选择逻辑分别处理。实现见 [DIRECT UDP](../../adapter/outbound/direct_udp.go)。
