# AndroidCyaml Integration

[返回功能目录](../features.md) · [配置示例](../config.yaml)

[androidcyaml](../../androidcyaml/doc.go) 提供宿主集成入口，涵盖生命周期、配置、网络、传输、进程和诊断。
当前接口版本为 `FacadeVersion = 3`，签名或语义改变需同步升级版本，让宿主在构建时发现不兼容。
AndroidCyaml 负责 VpnService、JNI、TUN 契约、socket protect 以及网络切换策略；核心负责映射内部操作。

物理网络身份和 IPv6 可用性由宿主提供，避免把 VPN TUN 自身地址误认为可用的物理 IPv6。
支持清理易失 DNS 数据并保留按网络隔离的长期候选，也可单独淘汰已退休网络的候选。
