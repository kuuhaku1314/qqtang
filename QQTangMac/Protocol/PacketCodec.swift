import Foundation

enum PacketCodec {
    static func encode(_ envelope: QQTEnvelope, sessionKey: Data?) throws -> Data {
        var payload = Data()
        payload.append(contentsOf: u16be(envelope.command.rawValue))
        payload.append(contentsOf: u32be(envelope.sequence))
        payload.append(envelope.payload)
        let body: Data
        if let sessionKey {
            body = try QQTEA.encrypt(payload, key: sessionKey)
        } else {
            body = payload
        }
        var frame = Data()
        frame.append(contentsOf: u32be(UInt32(body.count)))
        frame.append(body)
        return frame
    }

    static func decode(_ frame: Data, sessionKey: Data?) throws -> QQTEnvelope {
        guard frame.count >= 4 else { throw ProtocolError.framing("local game packet length is too short") }
        let length = Int(readU32be(frame, 0))
        guard frame.count >= 4 + length else {
            throw ProtocolError.framing("insufficient data for calculated length type")
        }
        var body = frame.subdata(in: 4..<(4 + length))
        if let sessionKey {
            body = try QQTEA.decrypt(body, key: sessionKey)
        }
        guard body.count >= 6 else { throw ProtocolError.framing("plaintext length is too short") }
        let commandRaw = readU16be(body, 0)
        guard let command = QQTCommand(rawValue: commandRaw) else {
            throw ProtocolError.framing(String(format: "local game command 0x%04X unsupported", commandRaw))
        }
        let sequence = readU32be(body, 2)
        let payload = body.count > 6 ? body.subdata(in: 6..<body.count) : Data()
        return QQTEnvelope(command: command, sequence: sequence, payload: payload)
    }

    static func encodeJSON<T: Encodable>(_ value: T) throws -> Data {
        try JSONEncoder().encode(value)
    }

    static func decodeJSON<T: Decodable>(_ type: T.Type, from data: Data) throws -> T {
        try JSONDecoder().decode(type, from: data)
    }

    private static func u16be(_ value: UInt16) -> [UInt8] {
        withUnsafeBytes(of: value.bigEndian) { Array($0) }
    }

    private static func u32be(_ value: UInt32) -> [UInt8] {
        withUnsafeBytes(of: value.bigEndian) { Array($0) }
    }

    private static func readU16be(_ data: Data, _ offset: Int) -> UInt16 {
        data.withUnsafeBytes { raw in
            UInt16(bigEndian: raw.loadUnaligned(fromByteOffset: offset, as: UInt16.self))
        }
    }

    private static func readU32be(_ data: Data, _ offset: Int) -> UInt32 {
        data.withUnsafeBytes { raw in
            UInt32(bigEndian: raw.loadUnaligned(fromByteOffset: offset, as: UInt32.self))
        }
    }
}
