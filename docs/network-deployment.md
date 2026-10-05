# 网络部署

下列启动命令和运行路径相对于完整解压后的发布包目录。Windows 可使用 `QQTang-Launcher.exe` 管理服务端和客户端，也可使用无界面启动脚本。

## 启动服务端

| 平台 | 启动脚本 |
| --- | --- |
| Windows AMD64 | `start-server-windows.cmd` |
| Linux AMD64 / x86_64 | `start-server-linux-amd64.sh` |
| Linux ARM64 / aarch64 | `start-server-linux-arm64.sh` |

Linux 示例：

```sh
chmod +x start-server-linux-*.sh
./start-server-linux-amd64.sh
```

ARM64 使用对应的 ARM64 脚本。脚本在前台运行，按 `Ctrl+C` 停止服务端；进程也支持 `SIGTERM`。

部署时保留包内的 `configs`、`data`、`runtime/client-patched` 和对应架构的 ONNX Runtime。服务端需要读取客户端静态配置，不能只复制单个可执行文件。

AI 启用且 `competitive_ai.backend` 为 `onnxruntime` 时，服务端在启动阶段加载一次运行库并共享推理会话。关闭 AI 或使用 `native` 后端时不加载 ONNX Runtime。`shared_library_path` 留空会按运行平台和架构选择包内运行库：Windows 使用 `.dll`，Linux 使用 `.so`；显式路径相对于服务端配置文件解析。

Windows 需要安装匹配服务端架构的 [Microsoft Visual C++ 运行库](https://learn.microsoft.com/cpp/windows/latest-supported-vc-redist)。AMD64 服务端使用 x64 版本。遇到 `DLL initialization routine failed` 时，先[下载微软官方 x64 运行库安装程序](https://aka.ms/vc14/vc_redist.x64.exe)，安装或修复后重启服务端；此错误也可能来自其他依赖初始化失败，不能仅凭错误判定系统版本不支持。若仍失败，请附上完整报错、CPU 型号和运行库版本。

## 网络配置

[configs/network.json](../configs/network.json) 将服务端监听模式和客户端连接目标分开设置。

| 字段 | 用途 |
| --- | --- |
| `schema_version` | 配置格式版本，当前为 `3` |
| `mode` | `local`、`lan-host` 或 `remote-host` |
| `server_ip` | 服务端向客户端公布的地址 |
| `client_server_ip` | 客户端连接的服务器地址，可独立于本机服务端设置 |
| `gm_remote` | 是否开放远程 GM，默认为 `false` |

| 模式 | 监听地址 | `server_ip` |
| --- | --- | --- |
| `local` | 配置的回环地址 | 默认 `127.0.0.1` |
| `lan-host` | `0.0.0.0` | 主机的局域网私有 IPv4 |
| `remote-host` | `0.0.0.0` | 玩家可访问的公网 IPv4 或域名 |

域名通过 IPv4 A 记录解析；原生目录协议向客户端提供 IPv4 地址。`client_server_ip` 支持 IPv4 或域名。修改配置后重启相应服务端或客户端。

例如，局域网主机地址为 `192.168.1.10` 时，主机配置为：

```json
{
  "schema_version": 3,
  "mode": "lan-host",
  "server_ip": "192.168.1.10",
  "client_server_ip": "192.168.1.10",
  "gm_remote": false
}
```

其他玩家在启动器中将客户端目标设置为 `192.168.1.10`，无需启动自己的服务端。

启动器的“服务器地址”也可以直接填域名（例如 `play.example.com`）。域名在每次点击“启动一个客户端”时重新解析为 IPv4 后写入客户端配置，因此服务器 IP 变更后无需修改地址，重新启动客户端即可连接新 IP；已在运行中的客户端不会自动切换。

## 端口与 GM

局域网或公网服务器需要放行 TCP `17000/18000/18001/18080/18443` 和 UDP `18000`。服务器位于路由器后时，还需将相应端口转发到服务器。客户端向中心服连接，无需在玩家之间配置端口映射。启动脚本不会自动修改防火墙规则。

GM 默认地址为 `http://127.0.0.1:18100/gm/`。启用 `gm_remote` 前设置管理账号和强密码；通过启动器设置，或使用服务端的 `-gm-username`、`-gm-password-stdin` 参数。凭据以 PBKDF2 加盐校验值保存在 `configs/gm-auth.json`。远程访问应通过 HTTPS 反向代理或可信隧道保护。

## 存档与日志

- 存档：`runtime/data/qqtang.sqlite`。升级或迁移前停止服务端并备份。
- 服务端日志：`runtime/logs/server-local.jsonl`；图形启动还生成 `local-server.stdout.log` 和 `local-server.stderr.log`。
- 客户端日志：`runtime/logs/local-client-launch*.json` 和 `local-launcher*.stderr.log`。

提交问题时附上复现步骤、系统版本和相关错误日志，并去除账号凭据及其他个人信息。
