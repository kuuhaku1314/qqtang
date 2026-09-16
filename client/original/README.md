# 原始客户端放置目录

本目录不随仓库分发原始 QQ堂客户端。请自行取得与本项目兼容的原始客户端，并把客户端根目录下的全部内容直接解压到这里。

放置完成后至少应当存在：

```text
client/original/Client.exe
client/original/Core.dll
client/original/QQTDir.dll
client/original/QQTModules.dll
client/original/QQTSection.dll
client/original/config/GameCFG.ini
```

当前静态补丁针对客户端主程序版本 `5.2.1.201`。构建过程只读取本目录，并先复制到 `release/<包名>/runtime/client-patched`；不会原地修改这里的文件。补丁工具会验证它实际修改的二进制位置，客户端版本不兼容时会明确停止。

不要把解压后的客户端文件提交到 Git。

## 找不到原版客户端时：使用已打过补丁的客户端

如果原版客户端已经找不到，可以用此前构建产物 `release/<包名>/runtime/client-patched`
（或据其运行过的客户端副本）作为基线。不要手工复制，使用导入脚本：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\import-patched-client.ps1 -Source <已打补丁客户端目录>
```

脚本会校验必需文件、清理游玩过程产生的账号与机器状态（多余的 profile 存档、
好友/亲密度数据、日志、下载缓存中损坏的资源包等），然后安装到本目录。构建脚本
通过 `Client.tp-free.json` 识别已打补丁的基线：静态二进制补丁自带幂等检测，端口等
精确替换在值已是目标值时直接通过；既不是原版旧值也不是补丁目标值时仍会明确报错。

注意：`devConfig.txt`、`disp_ogl.txt` 等显示配置会沿用来源客户端所在机器的状态
（每次打包本就固定分发基线中的这几个文件）。
