# Linux Endpoint-Aware Process Attribution

[返回功能目录](../features.md) · [配置示例](../config.yaml)

在 Linux 上使用本地和远端 socket endpoint 做精确进程归属查询，并缓存近期成功 PID 以减少 `/proc` 扫描。

Patches:

- `component/process.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
