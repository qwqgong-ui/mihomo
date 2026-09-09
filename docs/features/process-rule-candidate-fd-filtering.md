# Process-Rule Candidate FD Filtering

[返回功能目录](../features.md) · [配置示例](../config.yaml)

从 PROCESS、UID、logic、rule-provider 和 wrapper 规则构造候选 matcher，在 Linux 扫描文件描述符前过滤不可能命中的进程。

Patches:

- `component/process.patch`
- `rules/common.patch`
- `rules/logic.patch`
- `rules/provider.patch`
- `rules/wrapper.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
