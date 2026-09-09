# GODEBUG Defaults Released

[返回功能目录](../features.md) · [配置示例](../config.yaml)

`go 1.26` 引入时加的 `godebug` 块（把 24 项钉回 go1.20 默认值）已整块移除，
现在全部采用 go1.26 默认值。与之前相比实际发生变化的项：

| setting | 之前（go1.20 默认） | 现在（go1.26 默认） | 影响面 |
|---|---|---|---|
| `multipathtcp` | 0（关） | 2（listener 默认开 MPTCP） | 只波及不显式设置的监听器；两个 redirect 监听器已通过 [MPTCP Listener Guards](mptcp-listener-guards.md) 显式关闭，正常 inbound 由 `inbound-mptcp` 显式控制 |
| `tlssha1` | 1（允许） | 0（拒绝 TLS 1.2 的 SHA-1 签名） | 标准库 TLS 与证书校验 |
| `rsa1024min` | 0（允许） | 1（拒绝 <1024 位 RSA） | 同上 |
| `x509negativeserial` | 1（容忍） | 0（拒绝负序列号证书） | 同上 |
| `x509rsacrt` / `x509sha256skid` / `x509usepolicies` | 旧值 | 新值 | 证书解析与策略校验 |
| `tlsmlkem` / `tlssecpmlkem` | 0 | 1（默认启用后量子密钥交换） | 标准库 TLS 的 ClientHello |
| `httplaxcontentlength` | 1（容忍畸形 Content-Length） | 0（拒绝） | HTTP 客户端与服务端 |
| `httpservecontentkeepheaders` / `httpmuxgo121` | 旧语义 | 新语义 | `net/http` |
| `httpcookiemaxnum` / `urlmaxqueryparams` / `urlstrictcolons` | 0（无限制/宽松） | 新限制生效 | Go 1.26 新增的解析上限 |
| `panicnil` | 1（`panic(nil)` 不转换） | 0（转成 `*runtime.PanicNilError`） | 运行时 |
| `containermaxprocs` / `updatemaxprocs` | 0 | 1（cgroup 感知 GOMAXPROCS） | 容器内自动调整 P 数 |
| `cryptocustomrand` / `decoratemappings` / `randseednop` / `winsymlink` / `winreadlinkvolume` / `gotestjsonbuildtext` | 旧值 | 新值 | 低风险 |

代理出站 TLS 大多走 `metacubex/utls`，不受 `tls*` 这几项影响；但证书校验仍是
标准库 `crypto/x509`，所以 `rsa1024min`、`x509negativeserial`、`tlssha1` 会
影响到用弱参数或畸形证书的对端。真遇到问题，单独把对应项加回 `godebug` 块
即可，不必回退整个改动。
