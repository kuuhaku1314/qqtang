# Windows 本地协议还原（Mac 对等实现）

分析对象是发布包里的 `runtime/bin/qqt-server-local.exe`（Go 本地服），不是 `ClientBase.dll` 保护虚拟机。Mac 原生客户端不加载该 DLL；对等运行靠同一套 127.0.0.1 端口和会话机。

## 端口（与 `DirCfg.ini` / `server-directory-local-ui.json` 一致）

| 名称 | 地址 | 作用 |
|------|------|------|
| directory-http-local-ui | TCP 127.0.0.1:18080 | 收到任意请求后，回放官方 186 字节 `directory-response-east-official.bin`（`max_sends=1`） |
| directory-game-capture | TCP 127.0.0.1:18000 | 游戏会话：登录 / 房间 / 开局 / 事件中继 |
| directory-tls-capture | TCP 127.0.0.1:18443 | 捕获占位 |
| ca-secondary-capture | TCP 127.0.0.1:17000 | 捕获占位 |
| p2p-capture | UDP 127.0.0.1:18000 | P2P 占位 |

## 从 Go 服还原的会话机

```
connection_opened
  -> qqt_login_success
  -> qqt_second_followup
  -> qqt_room_list_empty
  -> qqt_player_list_empty
  -> qqt_create_room_success
  -> qqt_start_game_success
  -> qqt_adventure_game_begin
  -> qqt_game_event_relay / notify / ack
```

对应开关：`qqt_login_success`、`qqt_room_list_empty`、`qqt_player_list_empty`、`qqt_create_room_success`、`qqt_start_game_success`、`qqt_adventure_game`、`qqt_game_event_relay`。

## 命令名（uint16，Go 报错格式 `command 0x%04X`）

game-login、room-list、player-list、create-room、change-role、modify-room-info、start-game、map trigger、game-event、item-status-change、player-info refresh。

Mac 侧共享表见 `QQTangMac/Protocol/Opcode.swift`。官方 `Client.exe` 数值需用 Windows 抓包填回同一张表。

## 事件 schema（uint32，`game-event schema 0x%08X`）

`NOTIFY_PLAYER_DIE`、`NOTIFY_PLAYER_BE_KILLED`、`NOTIFY_PLAYER_BE_SAVED`、`NOTIFY_NPC_DROPITEM`、`NOTIFY_DISPATCH_ITEM`、`NOTIFY_GAME_OVER`、`GAME_BEGIN_DATA`、`REQUEST_GET_ITEM`、`REQUEST_KILL_PLAYER`、`REQUEST_SAVE_PLAYER`、`REQUEST_GAME_NEXTMAP`、`CREATE_PVENPC_BOSS`。

## 加密

Go 服明确：`QQ-TEA key length = 16`、`encrypt local game response`、`decrypt local game packet`、`QQ-TEA trailing zero validation failed`。登录成功前明文，之后用 16 字节会话密钥封包。

## ClientBase.dll

Windows 崩溃在保护虚拟机写只读系统内存（上游 issue #2）。Mac 不加载该 DLL，因此不需要在 macOS 上拆 VM。协议语义以 `qqt-server-local.exe` 为准；若要官方 opcode 数值，在 Windows 隔离环境抓 18000 流量对照本表即可。
