# Persisted DNS Answer Cache

[返回功能目录](../features.md) · [配置示例](../config.yaml)

DNS 应答缓存写入 `cache.db`（新 `dnscache` bucket），每小时一次加关机时 flush，
启动时载回。每条显式带过期时间：ARC 读取时不检查 `expires`，不带的话恢复后无法区分新鲜与陈旧；
带上之后，停机期间过期的条目会以陈旧状态恢复，由既有的乐观缓存路径返回一次并后台刷新。

载入必须由 `hub/executor` 在 DNS 就绪之后调用 `dns.LoadPersistentCache()` 触发，
**不能在 resolver 构造期间碰 `cachefile.Cache()`** —— 那是个单例，会把首次看到的路径永久锁定，
而此时运行时还没把 `C.Path` 指向 `-d` 目录，结果整个进程改去打开
`$HOME/.config/mihomo/cache.db`；在 systemd 下 root 无 HOME，打开失败、`DB` 为 nil，
fake-ip 池连带失去存储，所有 fake IP 反查失败。

写入用 `DB.Update` 而非 `DB.Batch`：`Batch` 会延迟提交以合并调用者，而关机路径在那之前就退出了。

`arc` 和 `lru` 两种 `cache-algorithm` 都支持，各自加了 `Snapshot()`：ARC 跳过 ghost 条目，
LRU 按最近最少使用顺序返回以便恢复时重放同样的 recency。两者的 item 类型不同，
`dns/cachestore.go` 里做归一化。

main / proxy-server / direct 三个 resolver 的缓存共用一个 bucket，键前缀区分，避免碰撞。

Patches:

- `common/arc.patch`
- `common/lru.patch`
- `component/profile.patch`
- `dns.patch`
- `hub/executor.patch`

> `Patches` 为迁移前的源码分组索引；实现与依赖补丁边界见[功能目录说明](../features.md)。
