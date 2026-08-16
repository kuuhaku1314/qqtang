import Foundation
import Network

final class ProtocolClient {
    private let queue = DispatchQueue(label: "qqt.game.client")
    private var connection: NWConnection?
    private var buffer = Data()
    private var sessionKey: Data?
    private var pending: PendingRequest?
    private var sequence: UInt32 = 1

    func connect(host: String = "127.0.0.1", port: UInt16 = 18000) async throws {
        let connection = NWConnection(host: NWEndpoint.Host(host), port: NWEndpoint.Port(rawValue: port)!, using: .tcp)
        self.connection = connection
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    connection.stateUpdateHandler = nil
                    continuation.resume()
                case .failed(let error):
                    continuation.resume(throwing: error)
                case .cancelled:
                    continuation.resume(throwing: ProtocolError.disconnected)
                default:
                    break
                }
            }
            connection.start(queue: self.queue)
        }
        receiveLoop()
    }

    func login(uin: Int64) async throws -> (QQTLoginSuccess, [QQTRoomSnapshot]) {
        let envelopes = try await transact(
            command: .gameLogin,
            payload: QQTLoginRequest(uin: uin),
            expected: 3
        )
        guard let login = envelopes.first(where: { $0.command == .loginSuccess }) else {
            throw ProtocolError.server("missing login success")
        }
        let success = try PacketCodec.decodeJSON(QQTLoginSuccess.self, from: login.payload)
        sessionKey = Data(hexString: success.sessionKeyHex)
        let rooms = envelopes
            .first(where: { $0.command == .roomListEmpty })
            .flatMap { try? PacketCodec.decodeJSON(QQTRoomListPayload.self, from: $0.payload).rooms }
            ?? []
        return (success, rooms)
    }

    func roomList() async throws -> [QQTRoomSnapshot] {
        let envelopes = try await transact(command: .roomList, payload: ["ok": true], expected: 1)
        return try PacketCodec.decodeJSON(QQTRoomListPayload.self, from: envelopes[0].payload).rooms
    }

    func createRoom(name: String, mapId: Int, channel: String) async throws -> QQTRoomSnapshot {
        let envelopes = try await transact(
            command: .createRoom,
            payload: QQTCreateRoomRequest(name: name, mapId: mapId, channel: channel),
            expected: 1
        )
        return try PacketCodec.decodeJSON(QQTRoomSnapshot.self, from: envelopes[0].payload)
    }

    func joinRoom(id: Int) async throws -> QQTRoomSnapshot {
        let envelopes = try await transact(
            command: .joinRoom,
            payload: QQTJoinRoomRequest(roomId: id),
            expected: 1
        )
        return try PacketCodec.decodeJSON(QQTRoomSnapshot.self, from: envelopes[0].payload)
    }

    func toggleReady() async throws -> QQTRoomSnapshot {
        let envelopes = try await transact(command: .readyStatus, payload: ["toggle": true], expected: 1)
        return try PacketCodec.decodeJSON(QQTRoomSnapshot.self, from: envelopes[0].payload)
    }

    func leaveRoom() async throws -> [QQTRoomSnapshot] {
        let envelopes = try await transact(command: .leaveRoom, payload: ["leave": true], expected: 1)
        return try PacketCodec.decodeJSON(QQTRoomListPayload.self, from: envelopes[0].payload).rooms
    }

    func startGame() async throws -> QQTStartGameSuccess {
        let envelopes = try await transact(command: .startGame, payload: ["start": true], expected: 2)
        guard let first = envelopes.first(where: { $0.command == .startGameSuccess }) ?? envelopes.first else {
            throw ProtocolError.server("missing start-game success")
        }
        return try PacketCodec.decodeJSON(QQTStartGameSuccess.self, from: first.payload)
    }

    func sendGameEvent(_ event: QQTGameEventPayload) async throws -> QQTGameEventPayload {
        let envelopes = try await transact(command: .gameEvent, payload: event, expected: 1)
        return try PacketCodec.decodeJSON(QQTGameEventPayload.self, from: envelopes[0].payload)
    }

    func exitBattle() async throws -> QQTRoomSnapshot {
        let envelopes = try await transact(command: .exitBattle, payload: ["exit": true], expected: 1)
        return try PacketCodec.decodeJSON(QQTRoomSnapshot.self, from: envelopes[0].payload)
    }

    private func transact<T: Encodable>(command: QQTCommand, payload: T, expected: Int) async throws -> [QQTEnvelope] {
        let seq = nextSequence()
        let envelope = QQTEnvelope(command: command, sequence: seq, payload: try PacketCodec.encodeJSON(payload))
        let frame = try PacketCodec.encode(envelope, sessionKey: sessionKey)
        guard let connection else { throw ProtocolError.disconnected }

        return try await withThrowingTaskGroup(of: [QQTEnvelope].self) { group in
            group.addTask {
                // 超时/取消时必须能解除挂起，否则任务组会永远等待这个子任务，
                // 造成「返回」等依赖协议的操作卡死。
                try await withTaskCancellationHandler {
                    try await withCheckedThrowingContinuation { continuation in
                        self.queue.async {
                            self.failPending(ProtocolError.server("请求被新请求取代"))
                            self.pending = PendingRequest(expected: expected, continuation: continuation)
                            connection.send(content: frame, completion: .contentProcessed { error in
                                if let error {
                                    self.failPending(error)
                                }
                            })
                        }
                    }
                } onCancel: {
                    self.queue.async {
                        self.failPending(ProtocolError.server("请求已取消"))
                    }
                }
            }
            group.addTask {
                try await Task.sleep(nanoseconds: 2_000_000_000)
                throw ProtocolError.server("本地协议超时")
            }
            do {
                let result = try await group.next()!
                group.cancelAll()
                return result
            } catch {
                group.cancelAll()
                throw error
            }
        }
    }

    private func nextSequence() -> UInt32 {
        let value = sequence
        sequence += 8
        return value
    }

    private func receiveLoop() {
        connection?.receive(minimumIncompleteLength: 1, maximumLength: 65_536) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            if let data { self.buffer.append(data) }
            self.drain()
            if isComplete || error != nil { return }
            self.receiveLoop()
        }
    }

    private func drain() {
        while buffer.count >= 4 {
            let length = buffer.withUnsafeBytes { raw in
                Int(UInt32(bigEndian: raw.loadUnaligned(as: UInt32.self)))
            }
            guard buffer.count >= 4 + length else { return }
            let frame = Data(buffer.prefix(4 + length))
            buffer.removeFirst(4 + length)
            do {
                let envelope = try PacketCodec.decode(frame, sessionKey: sessionKey)
                if envelope.command == .error {
                    let message = (try? PacketCodec.decodeJSON(QQTErrorPayload.self, from: envelope.payload))?.message
                    failPending(ProtocolError.server(message ?? "protocol error"))
                    continue
                }
                pending?.collected.append(envelope)
                if let pending, pending.collected.count >= pending.expected {
                    self.pending = nil
                    pending.continuation.resume(returning: pending.collected)
                }
            } catch {
                failPending(error)
            }
        }
    }

    private func failPending(_ error: Error) {
        if let pending {
            self.pending = nil
            pending.continuation.resume(throwing: error)
        }
    }
}

private final class PendingRequest {
    let expected: Int
    var collected: [QQTEnvelope] = []
    let continuation: CheckedContinuation<[QQTEnvelope], Error>

    init(expected: Int, continuation: CheckedContinuation<[QQTEnvelope], Error>) {
        self.expected = expected
        self.continuation = continuation
    }
}

private extension Data {
    init?(hexString: String) {
        let cleaned = hexString.replacingOccurrences(of: " ", with: "")
        guard cleaned.count % 2 == 0 else { return nil }
        var data = Data()
        var index = cleaned.startIndex
        while index < cleaned.endIndex {
            let next = cleaned.index(index, offsetBy: 2)
            guard let byte = UInt8(cleaned[index..<next], radix: 16) else { return nil }
            data.append(byte)
            index = next
        }
        self = data
    }
}
