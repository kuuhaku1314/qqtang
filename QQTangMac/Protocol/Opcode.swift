import Foundation

/// Commands recovered from Windows `qqt-server-local.exe` string table.
/// Values are the Mac local-session IDs shared by client and server.
/// Official Client.exe numbers can be remapped here once a Windows capture is available.
enum QQTCommand: UInt16, Codable, CaseIterable {
    case gameLogin = 0x0101
    case loginSuccess = 0x0102
    case secondFollowup = 0x0103
    case roomList = 0x0201
    case roomListEmpty = 0x0202
    case playerList = 0x0203
    case playerListEmpty = 0x0204
    case createRoom = 0x0301
    case createRoomSuccess = 0x0302
    case joinRoom = 0x0303
    case leaveRoom = 0x0304
    case modifyRoomInfo = 0x0305
    case changeRole = 0x0306
    case readyStatus = 0x0307
    case startGame = 0x0401
    case startGameSuccess = 0x0402
    case adventureGameBegin = 0x0403
    case gameEvent = 0x0501
    case gameEventNotify = 0x0502
    case gameEventAck = 0x0503
    case exitBattle = 0x0504
    case error = 0x00FF
}

enum QQTEventSchema: UInt32, Codable {
    case notifyPlayerDie = 0x0001_0001
    case notifyPlayerBeKilled = 0x0001_0002
    case notifyPlayerBeSaved = 0x0001_0003
    case notifyNpcDropItem = 0x0001_0004
    case notifyDispatchItem = 0x0001_0005
    case notifyGameOver = 0x0001_0006
    case notifyGameEvent = 0x0001_0007
    case requestGetItem = 0x0002_0001
    case requestKillPlayer = 0x0002_0002
    case requestSavePlayer = 0x0002_0003
    case requestGameNextMap = 0x0002_0004
    case createPveNpcBoss = 0x0002_0005
    case gameBeginData = 0x0003_0001
    case preparedUseProp = 0x0003_0002
    case placeBomb = 0x0003_0010
}

struct QQTEnvelope: Codable, Equatable {
    var command: QQTCommand
    var sequence: UInt32
    var payload: Data

    enum CodingKeys: String, CodingKey {
        case command, sequence, payload
    }
}

struct QQTErrorPayload: Codable {
    var message: String
}

struct QQTLoginRequest: Codable {
    var uin: Int64
}

struct QQTLoginSuccess: Codable {
    var uin: Int64
    var nickname: String
    var level: Int
    var adventureCards: Int
    var staminaPotions: Int
    var sessionKeyHex: String
}

struct QQTRoomSnapshot: Codable, Equatable {
    var id: Int
    var name: String
    var mapId: Int
    var mapFile: String
    var mapName: String
    var channel: String
    var status: String
    var seats: [QQTSeatSnapshot]
}

struct QQTSeatSnapshot: Codable, Equatable {
    var index: Int
    var uid: Int64?
    var nickname: String?
    var team: Int
    var isReady: Bool
    var isHost: Bool
    var characterCode: String?
}

struct QQTRoomListPayload: Codable {
    var rooms: [QQTRoomSnapshot]
}

struct QQTCreateRoomRequest: Codable {
    var name: String
    var mapId: Int
    var channel: String
}

struct QQTJoinRoomRequest: Codable {
    var roomId: Int
}

struct QQTStartGameSuccess: Codable {
    var room: QQTRoomSnapshot
    var mapId: Int
}

struct QQTGameEventPayload: Codable {
    var schema: QQTEventSchema
    var text: String
    var roomId: Int?
}
