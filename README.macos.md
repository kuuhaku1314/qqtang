# QQ堂 Mac 怀旧复活版

基于 [kuuhaku1314/qqtang](https://github.com/kuuhaku1314/qqtang) 的本地包，在 macOS 上复活 QQ堂：读取原版 `.img` / `.map` / 配置与协议目录，用 SwiftUI + SpriteKit 重写客户端。

## 第一步：导入原版资源

```bash
./scripts/import-gamedata.sh
```

会下载并解压 `QQTang-Local.rar`，链接到 `GameData/client-patched`，并导出：

- `GameData/catalog/maps.json` — 382 张地图（来自 `mapDesc.py`）
- `GameData/catalog/protocol-local-ui.json` — 本地 directory 服配置（端口、背包种子、协议开关）

要把完整素材提交进 Git：先运行 import，再把 symlink 换成实体拷贝：

```bash
rsync -a GameData/client-patched/ GameData/client-patched.bak/
rm GameData/client-patched
mv GameData/client-patched.bak GameData/client-patched
git add GameData/catalog GameData/client-patched
```

## 构建与运行

```bash
xcodegen generate
xcodebuild -scheme QQTangMac -configuration Debug -destination 'platform=macOS' build
open ~/Library/Developer/Xcode/DerivedData/QQTangMac-*/Build/Products/Debug/QQTangMac.app
```

或在 Xcode 中 **Run (⌘R)**。

## 已实现

- QQF/DIMG 贴图解码（RGB565 + Alpha）
- 登录页 `bg_login.img` 背景
- 大厅地图选择器 + 地图缩略图预览
- 对战场加载原版 `map/*.img` 贴图
- 读取 `server-directory-local-ui.json` 的种子背包与本地端口配置
- SQLite 本地存档（账号 1000001、探险卡/药水数量对齐原版配置）

## 进行中 / 下一步

- 完整解析 `.map` 碰撞与 `mapElem.py` 元素
- 用 Windows 抓包把 `Opcode.swift` 填成官方 `Client.exe` 数值
- 角色 sprite、音效 `.wav` / `.ogg`

协议说明见 [docs/PROTOCOL.md](docs/PROTOCOL.md)。

## 存档路径

```
~/Library/Application Support/QQTangMac/qqtang.sqlite
```

## 资源查找顺序

1. 环境变量 `QQTANG_GAMEDATA`
2. 仓库 `GameData/client-patched`
3. `~/Library/Application Support/QQTangMac/client-patched`
