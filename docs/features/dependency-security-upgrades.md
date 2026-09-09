# Dependency Security Upgrades

[返回功能目录](../features.md) · [配置示例](../config.yaml)

go 指令抬到 1.26 之后可以取到的更新。govulncheck 符号级扫描（`-mode=binary`）
从 **4 个可达漏洞降到 0**，模块层面的不可达项从 29 降到 1。

修掉的可达项：

| | 模块 | 修复版本 | 可达符号 |
|---|---|---|---|
| GO-2026-4918 | x/net | v0.53.0 | `http2.Transport.RoundTrip` —— 对端发畸形 `SETTINGS_MAX_FRAME_SIZE` 让 HTTP/2 客户端死循环 |
| GO-2026-5026 | x/net | v0.55.0 | `idna.ToASCII` —— Punycode 标签校验绕过 |
| GO-2025-3503 | x/net | v0.36.0 | `httpproxy.config.useProxy` —— IPv6 Zone ID 代理绕过 |
| GO-2026-5970 | x/text | v0.39.0 | `norm.Form.*` —— 畸形输入死循环 |

另外顺带修掉两个不可达项：`klauspost/compress` 的 s2 OOB read（GO-2026-5841）
和 `insomniacslk/dhcp` 的畸形 IPv4 包 DoS（GO-2026-6237）。

版本变动：

```
github.com/insomniacslk/dhcp  20250109 -> 20260728
github.com/klauspost/compress v1.17.9  -> v1.19.2
github.com/miekg/dns          v1.1.63  -> v1.1.73
golang.org/x/crypto           v0.33.0  -> v0.55.0
golang.org/x/net              v0.35.0  -> v0.58.0
golang.org/x/sync             v0.11.0  -> v0.22.0
golang.org/x/sys              v0.30.0  -> v0.47.0
golang.org/x/mod/term/text/tools                    (indirect，随之拉起)
```

剩下唯一一项是 GO-2026-5932（`x/crypto/openpgp` 已废弃，无修复版本），不可达。
