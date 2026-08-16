# GameData

原版客户端资源目录。运行：

```bash
./scripts/import-gamedata.sh
```

会把 [kuuhaku1314/qqtang](https://github.com/kuuhaku1314/qqtang) 解压包里的 `runtime/client-patched` 链接到 `GameData/client-patched`，并导出 `catalog/maps.json` 与 `catalog/protocol-local-ui.json`。

Mac 客户端启动时会按以下顺序查找资源：

1. 环境变量 `QQTANG_GAMEDATA`
2. 仓库内 `GameData/client-patched`
3. `~/Library/Application Support/QQTangMac/client-patched`

这些素材来自已停服的原版 QQ堂 客户端包，仅用于非商业怀旧复活项目。
