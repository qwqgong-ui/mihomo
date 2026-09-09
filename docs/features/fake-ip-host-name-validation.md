# Fake-IP Host-Name Validation

[返回功能目录](../features.md) · [配置示例](../config.yaml)

Fake-IP 的 A/AAAA 不问上游，客户端查什么都给地址，包括没有任何域名服务器会解析的
字符串 —— 直接喂给 ping 或 curl 的 URL 就是这样。拿到的地址对所有协议都是死的：
DIRECT 在出站解析该名字时同样失败，用户看到的却像是路由问题，其实是个笔误。合成
之前先检查名字能不能是个主机名，不能就回 NXDOMAIN，把失败摆回它真正发生的地方，
也和不挂隧道时看到的结果一致（`名称或服务未知`）。

判据是字符而不是注册名：下划线（`_dmarc`、`_http._tcp`）、单标签（局域网主机名）
和 punycode 都是正当查询；而 miekg/dns 的呈现形式里出现转义序列，就意味着线格式
里带了主机名不可能有的字节。只作用于 Fake-IP 合成的 A/AAAA，其他类型不变。

Patches:

- `dns.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
