# Optional Protocol Build Tags

[返回功能目录](../features.md) · [配置示例](../config.yaml)

为 WireGuard、OpenVPN、Mieru 和 Sudoku 增加可选构建标签及 parser-compatible stub，并保留共享 IP stack 所需实现。
WireGuard 实现只在未使用 `no_wireguard` 时编译；裁剪构建使用同名 stub 保留配置解析类型，
但会在实例化时返回明确的功能已禁用错误。WireGuard 协议实现本身完整保留在
`wireguard.go`。`ipstack.go` 是 MASQUE、OpenVPN、WireGuard 和 ZeroTier 共用的网络栈抽象，
因此不能放进受 `!no_wireguard` 控制的文件；只有 gVisor 构造器再按 `with_gvisor` 分文件编译。

Patches:

- `adapter/outbound.patch`
- `common/convert.patch`
- `component/generator.patch`
- `constant/features.patch`
- `listener/inbound.patch`
- `listener/mieru.patch`
- `listener/sudoku.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
