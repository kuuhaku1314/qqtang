# 参与开发

公开仓库面向服务端、启动器、协议实现和静态客户端兼容补丁的开发。原始客户端、账号数据、日志、抓包和个人运行状态不得提交。

提交前从仓库根目录运行：

```powershell
go test ./... -count=1
go vet ./...
go build ./cmd/...
```

修复应以源码、协议、抓包或可复现实验为依据。不要用现象猜测协议，也不要加入与当前风险无关的限制和配置。

完整发行包构建还需要：

- Go 1.26；
- Python 3.10 或更新版本；
- Windows AMD64 GCC（ONNX Runtime 的 CGO 构建）；
- .NET Framework 64 位 C# 编译器；
- 构建 Linux 二进制时所需的 AMD64、ARM64 交叉 GCC。

只需要 Windows 产物时，可运行
`scripts\build-release.ps1 -WindowsOnly`：跳过 Linux ONNX Runtime 下载、
Linux 服务端交叉编译及 Linux 运行时/启动脚本载荷，因此不需要上述交叉 GCC；
Windows AMD64 GCC 仍然是必需的（Windows 服务端的 ONNX Runtime CGO 构建）。

两个静态客户端适配器的 NASM 源码位于 `scripts/asm/`，已生成的 4 KiB
载荷随 Go 源码一同版本化，普通构建不要求安装 NASM。修改汇编后安装 NASM 并运行：

```powershell
go generate ./internal/tooling/clientpatch
```

原始客户端的准备方法见 [client/original/README.md](client/original/README.md)，构建资源说明见 [build-assets/README.md](build-assets/README.md)。
