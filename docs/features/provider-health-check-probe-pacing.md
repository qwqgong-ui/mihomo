# Provider Health-Check Probe Pacing

[返回功能目录](../features.md) · [配置示例](../config.yaml)

在 provider 之间共享健康检查启动节拍，以随机短间隔错开 probe，同时保留既有并发上限。

Patches:

- `adapter/provider.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
