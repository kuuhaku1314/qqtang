import Foundation
import Network

/// Mirrors Windows `directory-game-capture` on 127.0.0.1:18000.
/// Session machine recovered from `qqt-server-local.exe`:
/// login success -> room list -> create/join room -> start game -> game-event relay.
final class GameServer {
    private var listener: NWListener?
    private let queue = DispatchQueue(label: "qqt.game.server")
    private var rooms: [Int: QQTRoomSnapshot] = [:]
    private var nextRoomID = 1001
    private var nextSequence: UInt32 = 1

    func start(port: UInt16 = 18000) throws {
        seedLobby()
        let parameters = NWParameters.tcp
        parameters.allowLocalEndpointReuse = true
        listener = try NWListener(using: parameters, on: NWEndpoint.Port(rawValue: port)!)
        listener?.newConnectionHandler = { [weak self] connection in
            self?.accept(connection)
        }
        listener?.start(queue: queue)
    }

    func stop() {
        listener?.cancel()
        listener = nil
    }

    private func accept(_ connection: NWConnection) {
        let session = GameConnection(connection: connection, server: self)
        connection.start(queue: queue)
        session.receiveLoop()
    }

    fileprivate func handle(_ envelope: QQTEnvelope, session: GameConnection) throws -> [QQTEnvelope] {
        switch envelope.command {
        case .gameLogin:
            let request = try PacketCodec.decodeJSON(QQTLoginRequest.self, from: envelope.payload)
            let account = try DatabaseManager.shared.fetchAccount(uid: request.uin)
            session.uin = account.uid
            session.nickname = account.nickname
            session.pendingSessionKey = QQTEA.sessionKey(for: account.uid)
            let success = QQTLoginSuccess(
                uin: account.uid,
                nickname: account.nickname,
                level: account.level,
                adventureCards: account.adventureCards,
                staminaPotions: account.staminaPotions,
                sessionKeyHex: session.pendingSessionKey!.map { String(format: "%02x", $0) }.joined()
            )
            return [
                reply(.loginSuccess, envelope.sequence, success),
                reply(.roomListEmpty, envelope.sequence &+ 1, QQTRoomListPayload(rooms: Array(rooms.values))),
                reply(.playerListEmpty, envelope.sequence &+ 2, QQTRoomListPayload(rooms: []))
            ]
        case .roomList:
            return [reply(.roomListEmpty, envelope.sequence, QQTRoomListPayload(rooms: Array(rooms.values)))]
        case .createRoom:
            guard let uin = session.uin, let nickname = session.nickname else {
                throw ProtocolError.server("cannot create room without a session player")
            }
            let request = try PacketCodec.decodeJSON(QQTCreateRoomRequest.self, from: envelope.payload)
            let map = MapCatalog.entry(id: request.mapId) ?? MapCatalog.defaultEntry
            var room = emptyRoom(id: nextRoomID, name: request.name, map: map, channel: request.channel)
            nextRoomID += 1
            sit(&room, uid: uin, nickname: nickname, asHost: true)
            rooms[room.id] = room
            session.roomID = room.id
            return [reply(.createRoomSuccess, envelope.sequence, room)]
        case .joinRoom:
            guard let uin = session.uin, let nickname = session.nickname else {
                throw ProtocolError.server("cannot join room without a session player")
            }
            let request = try PacketCodec.decodeJSON(QQTJoinRoomRequest.self, from: envelope.payload)
            guard var room = rooms[request.roomId] else { throw ProtocolError.server("room not found") }
            if !room.seats.contains(where: { $0.uid == uin }) {
                sit(&room, uid: uin, nickname: nickname, asHost: room.seats.allSatisfy { $0.uid == nil })
            }
            rooms[room.id] = room
            session.roomID = room.id
            return [reply(.createRoomSuccess, envelope.sequence, room)]
        case .readyStatus:
            guard let uin = session.uin, var room = currentRoom(session) else {
                throw ProtocolError.server("session is not in a room")
            }
            if let index = room.seats.firstIndex(where: { $0.uid == uin }) {
                room.seats[index].isReady.toggle()
            }
            rooms[room.id] = room
            return [reply(.readyStatus, envelope.sequence, room)]
        case .leaveRoom:
            if let uin = session.uin, var room = currentRoom(session) {
                room.seats = room.seats.map { seat in
                    var next = seat
                    if next.uid == uin {
                        next.uid = nil
                        next.nickname = nil
                        next.isReady = false
                        next.isHost = false
                    }
                    return next
                }
                if room.seats.allSatisfy({ $0.uid == nil }) {
                    rooms.removeValue(forKey: room.id)
                } else {
                    rooms[room.id] = room
                }
            }
            session.roomID = nil
            return [reply(.roomListEmpty, envelope.sequence, QQTRoomListPayload(rooms: Array(rooms.values)))]
        case .startGame:
            guard let uin = session.uin, var room = currentRoom(session) else {
                throw ProtocolError.server("session is not in a room")
            }
            guard room.seats.first(where: { $0.uid == uin })?.isHost == true else {
                throw ProtocolError.server("player \(uin) is not room \(room.id) owner")
            }
            let occupied = room.seats.filter { $0.uid != nil }
            guard !occupied.isEmpty, occupied.allSatisfy(\.isReady) else {
                throw ProtocolError.server("room \(room.id) is not preparing")
            }
            room.status = "playing"
            rooms[room.id] = room
            let begin = QQTStartGameSuccess(room: room, mapId: room.mapId)
            return [
                reply(.startGameSuccess, envelope.sequence, begin),
                reply(.adventureGameBegin, envelope.sequence &+ 1, begin)
            ]
        case .gameEvent:
            let event = try PacketCodec.decodeJSON(QQTGameEventPayload.self, from: envelope.payload)
            return [reply(.gameEventNotify, envelope.sequence, event)]
        case .exitBattle:
            if var room = currentRoom(session) {
                room.status = "waiting"
                room.seats = room.seats.map { seat in
                    var next = seat
                    next.isReady = false
                    return next
                }
                rooms[room.id] = room
                return [reply(.createRoomSuccess, envelope.sequence, room)]
            }
            return [reply(.roomListEmpty, envelope.sequence, QQTRoomListPayload(rooms: Array(rooms.values)))]
        default:
            throw ProtocolError.server(String(format: "local game command 0x%04X unsupported", envelope.command.rawValue))
        }
    }

    private func currentRoom(_ session: GameConnection) -> QQTRoomSnapshot? {
        guard let id = session.roomID else { return nil }
        return rooms[id]
    }

    private func reply<T: Encodable>(_ command: QQTCommand, _ sequence: UInt32, _ payload: T) -> QQTEnvelope {
        let data = (try? PacketCodec.encodeJSON(payload)) ?? Data()
        return QQTEnvelope(command: command, sequence: sequence, payload: data)
    }

    private func seedLobby() {
        let catalog = MapCatalog.load().filter { $0.maxPlayers >= 2 }
        let fallback = MapCatalog.defaultEntry
        let maps = catalog.isEmpty ? [fallback] : Array(catalog.prefix(8))
        var seeded: [Int: QQTRoomSnapshot] = [:]
        for index in 0..<8 {
            let map = maps[index % maps.count]
            seeded[101 + index] = emptyRoom(
                id: 101 + index,
                name: index == 0 ? "\(map.name) 新手房" : "\(map.name) \(index + 1)号房",
                map: map,
                channel: "休闲"
            )
        }
        seeded[201] = emptyRoom(
            id: 201,
            name: "高手竞技场",
            map: maps[0],
            channel: "竞技"
        )
        rooms = seeded
    }

    private func emptyRoom(id: Int, name: String, map: LegacyMapEntry, channel: String) -> QQTRoomSnapshot {
        let seats = (1...8).map { index in
            QQTSeatSnapshot(index: index, uid: nil, nickname: nil, team: index <= 4 ? 0 : 1, isReady: false, isHost: false)
        }
        return QQTRoomSnapshot(
            id: id,
            name: name,
            mapId: map.id,
            mapFile: map.mapFile,
            mapName: map.name,
            channel: channel,
            status: "waiting",
            seats: seats
        )
    }

    private func sit(_ room: inout QQTRoomSnapshot, uid: Int64, nickname: String, asHost: Bool) {
        guard let index = room.seats.firstIndex(where: { $0.uid == nil }) else { return }
        room.seats[index].uid = uid
        room.seats[index].nickname = nickname
        room.seats[index].isReady = false
        room.seats[index].isHost = asHost
    }
}

final class GameConnection {
    let connection: NWConnection
    weak var server: GameServer?
    var uin: Int64?
    var nickname: String?
    var roomID: Int?
    var sessionKey: Data?
    var pendingSessionKey: Data?
    private var buffer = Data()

    init(connection: NWConnection, server: GameServer) {
        self.connection = connection
        self.server = server
    }

    func receiveLoop() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65_536) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            if let data { self.buffer.append(data) }
            self.drain()
            if isComplete || error != nil {
                self.connection.cancel()
                return
            }
            self.receiveLoop()
        }
    }

    private func drain() {
        while buffer.count >= 4 {
            let length = buffer.withUnsafeBytes { raw in
                Int(UInt32(bigEndian: raw.loadUnaligned(as: UInt32.self)))
            }
            guard buffer.count >= 4 + length else { return }
            let frame = buffer.prefix(4 + length)
            buffer.removeFirst(4 + length)
            do {
                let request = try PacketCodec.decode(Data(frame), sessionKey: sessionKey)
                let replies = try server?.handle(request, session: self) ?? []
                for reply in replies {
                    let encoded = try PacketCodec.encode(reply, sessionKey: sessionKey)
                    connection.send(content: encoded, completion: .contentProcessed { _ in })
                }
                if let pendingSessionKey {
                    sessionKey = pendingSessionKey
                    self.pendingSessionKey = nil
                }
            } catch {
                let payload = (try? PacketCodec.encodeJSON(QQTErrorPayload(message: error.localizedDescription))) ?? Data()
                let envelope = QQTEnvelope(command: .error, sequence: 0, payload: payload)
                if let encoded = try? PacketCodec.encode(envelope, sessionKey: sessionKey) {
                    connection.send(content: encoded, completion: .contentProcessed { _ in })
                }
            }
        }
    }
}
