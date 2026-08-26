# A/B 基准

不参与打补丁（`apply-dependency-patches.sh` 只 glob `*.patch` 和 `files/`）。
把这两个文件拷进打好补丁的 sing-tun 模块目录再跑：

    GOEXPERIMENT=simd GOAMD64=v3 go test -run XXX -bench . -benchtime 300ms .
    go test -run TestWriteSyscallCount -v .

每项都把补丁前的实现留在文件里（`legacyTCPNat`、`BatchWrite` 单包路径、
`handleGRO(trustCSum=false)`），所以是同一进程内的对拍，不受机器状态影响。
