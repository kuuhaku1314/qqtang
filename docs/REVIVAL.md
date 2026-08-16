# 复活路线说明

## 当前策略

Mac 版**不运行** Windows `Client.exe`，因此不需要在 macOS 上绕过 `ClientBase.dll` 的 WOW64 保护。复活路径是：

1. 解包 `QQTang-Local.rar` 取得 `runtime/client-patched` 资源与配置
2. 逆向明文格式：`QQF/DIMG`、`mapDesc.py`、`.map` 元素表、`server-directory-local-ui.json`
3. 用 Swift 重写 UI 与对战场，逐步接入原版协议包

## 已逆向格式

| 格式 | 结论 |
|------|------|
| `.img` | 魔数 `QQF\\x1aDIMG`，偏移 0x20/0x24 为宽高，0x44 起 RGB565，之后 Alpha8 |
| `mapDesc.py` | GBK Python 元组，382 条地图记录 |
| `server-directory-local-ui.json` | 本地 directory 监听 18080/18000 等，含玩家档案与背包种子 |
| `.map` | 头部 spawn 参数 + 元素 ID 列表 + 尾部序列化层（碰撞解析进行中） |

## ClientBase.dll 与协议

Mac 不加载 `ClientBase.dll`。对等运行靠从 `qqt-server-local.exe` 还原的本地协议（见 [PROTOCOL.md](PROTOCOL.md)）：

- 18080 回放官方 186 字节 directory 包
- 18000 登录 / 房间 / 开局 / 事件中继
- 登录后 QQ-TEA（16 字节密钥）

`ClientBase.dll` 的保护虚拟机只影响 Windows `Client.exe` 启动，不参与 Mac 原生会话。

## 素材进仓库

`GameData/client-patched` 默认是指向 `.vendor` 的 symlink（开发用）。要完整入库请见根目录 README 的 `rsync` 步骤。
