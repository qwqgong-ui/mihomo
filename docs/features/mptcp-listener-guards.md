# MPTCP Listener Guards

[返回功能目录](../features.md) · [配置示例](../config.yaml)

`listener/tproxy` 早就显式 `SetMultipathTCP(false)`，理由写在注释里：Go 1.24 起
listener 默认开 MPTCP，会让 tproxy 在某些内核上失效。同类的两处漏了，本分支补上：
`listener/redir`（裸 `net.Listen`）和 sing-tun 的 redirect server（补丁 `0006`）。

实测（Linux 7.2，`net.mptcp.enabled=1`）：

```
plain TCP   conn_mptcp=false  SO_ORIGINAL_DST -> 127.0.0.1:21077
MPTCP       conn_mptcp=true   SO_ORIGINAL_DST -> ERROR operation not supported
```

要开 inbound MPTCP 用配置项 `inbound-mptcp: true` —— 走
`adapter/inbound.ListenConfig` 的显式设置，对每个正常 inbound 生效，而
`net.ListenConfig` 的 MPTCP 是三态，显式值优先于 GODEBUG，所以
`multipathtcp=0` 钉着也照样能建立真 MPTCP 连接（实测 `conn_mptcp=true`）。
换句话说放开 `multipathtcp` 这个 GODEBUG 并不会让正常 inbound 多拿到什么，
只会波及这些不显式设置的监听器。
