# Go 1.26 Language Version

[返回功能目录](../features.md) · [配置示例](../config.yaml)

`go.mod` 的 go 指令从上游的 `go 1.20` 抬到 `go 1.26`。上游整套 fork
（sing、sing-tun、quic-go）都停在 1.20，所以这是一处会在每次 upstream sync
PR 里冲突的分歧，合并时保留下游的 go 指令。

抬升的动机是依赖：`golang.org/x/net`、`golang.org/x/text`、`golang.org/x/crypto`
和 `miekg/dns` 的当前版本都声明 `go 1.25.0`，主模块停在 1.20 就无法 require
它们，也就拿不到 x/net 的 HTTP/2 与 idna 安全修复。依赖升级见 [Dependency Security Upgrades](dependency-security-upgrades.md)。

本改动只动语言版本，不改运行时行为：

- go 指令一抬，26 个原本被钉在 go1.20 默认值上的 GODEBUG 会全部释放。抬升时
  先用 `godebug` 块把它们钉回原值以隔离风险，之后整块移除，现在全部采用
  go1.26 默认值，见 [GODEBUG Defaults Released](godebug-defaults-released.md)。
- 唯一无法用 `godebug` 钉住的是 Go 1.22 的循环变量按迭代语义。用
  `-gcflags=github.com/metacubex/mihomo/...=-d=loopvar=2` 量化过：本仓库自身代码
  只有 4 个循环受影响，全部 stack-allocated（循环变量没有被闭包或 goroutine
  捕获），可观察差异为 0。依赖仍声明 go 1.20，不受影响。
- Go 1.24 起 `printf` 分析器会检查非常量格式串，`go test` 默认跑 vet，
  因此 25 处「把运行时字符串当格式串传」的调用必须先修，否则 CI 直接红。
  这些是真问题：消息里含 `%` 时会输出成 `%!x(MISSING)`。
