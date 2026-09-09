# Physical-Network DNS Dialing

[返回功能目录](../features.md) · [配置示例](../config.yaml)

发现系统的物理网络 DNS，绑定对应接口，并使用 sing-tun auto-redirect output mark 绕过 TUN。

Patches:

- `component/dialer.patch`
- `dns.patch`
- `docs.patch`
- `listener/sing_tun.patch`
- `tunnel.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
