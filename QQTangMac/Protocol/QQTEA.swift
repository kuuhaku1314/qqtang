import Foundation

/// QQ-TEA as used by the published Windows local server (`QQ-TEA key length = 16`,
/// trailing-zero validation). This is the public Tencent TEA variant for packet
/// bodies, not a ClientBase.dll unpacker.
enum QQTEA {
    static let keyLength = 16

    static func encrypt(_ plain: Data, key: Data) throws -> Data {
        let schedule = try keySchedule(key)
        let padded = pad(plain)
        var dest = Data()
        dest.reserveCapacity(padded.count)
        var previous = [UInt32](repeating: 0, count: 2)
        var offset = 0
        while offset < padded.count {
            var block = [
                readUInt32BE(padded, offset),
                readUInt32BE(padded, offset + 4)
            ]
            block[0] ^= previous[0]
            block[1] ^= previous[1]
            encryptBlock(&block, schedule)
            previous = block
            dest.append(contentsOf: bytesBE(block[0]))
            dest.append(contentsOf: bytesBE(block[1]))
            offset += 8
        }
        return dest
    }

    static func decrypt(_ cipher: Data, key: Data) throws -> Data {
        guard cipher.count >= 16, cipher.count % 8 == 0 else {
            throw ProtocolError.tea("ciphertext length \(cipher.count) is invalid")
        }
        let schedule = try keySchedule(key)
        var dest = Data()
        dest.reserveCapacity(cipher.count)
        var previous = [UInt32](repeating: 0, count: 2)
        var offset = 0
        while offset < cipher.count {
            var block = [
                readUInt32BE(cipher, offset),
                readUInt32BE(cipher, offset + 4)
            ]
            let current = block
            decryptBlock(&block, schedule)
            block[0] ^= previous[0]
            block[1] ^= previous[1]
            previous = current
            dest.append(contentsOf: bytesBE(block[0]))
            dest.append(contentsOf: bytesBE(block[1]))
            offset += 8
        }
        return try unpad(dest)
    }

    static func sessionKey(for uin: Int64) -> Data {
        var material = "QQTang-Local-Session-\(uin)".data(using: .utf8) ?? Data()
        while material.count < keyLength { material.append(0) }
        return material.prefix(keyLength)
    }

    private static func keySchedule(_ key: Data) throws -> [UInt32] {
        guard key.count == keyLength else {
            throw ProtocolError.tea("QQ-TEA key length = \(key.count), want 16")
        }
        return [
            readUInt32BE(key, 0),
            readUInt32BE(key, 4),
            readUInt32BE(key, 8),
            readUInt32BE(key, 12)
        ]
    }

    private static func encryptBlock(_ v: inout [UInt32], _ k: [UInt32]) {
        var v0 = v[0]
        var v1 = v[1]
        var sum: UInt32 = 0
        let delta: UInt32 = 0x9E3779B9
        for _ in 0..<16 {
            sum &+= delta
            v0 &+= ((v1 << 4) &+ k[0]) ^ (v1 &+ sum) ^ ((v1 >> 5) &+ k[1])
            v1 &+= ((v0 << 4) &+ k[2]) ^ (v0 &+ sum) ^ ((v0 >> 5) &+ k[3])
        }
        v[0] = v0
        v[1] = v1
    }

    private static func decryptBlock(_ v: inout [UInt32], _ k: [UInt32]) {
        var v0 = v[0]
        var v1 = v[1]
        let delta: UInt32 = 0x9E3779B9
        var sum: UInt32 = delta &* 16
        for _ in 0..<16 {
            v1 &-= ((v0 << 4) &+ k[2]) ^ (v0 &+ sum) ^ ((v0 >> 5) &+ k[3])
            v0 &-= ((v1 << 4) &+ k[0]) ^ (v1 &+ sum) ^ ((v1 >> 5) &+ k[1])
            sum &-= delta
        }
        v[0] = v0
        v[1] = v1
    }

    private static func pad(_ plain: Data) -> Data {
        var body = Data([UInt8.random(in: 0...255) & 0xF8 | 0x02])
        let headerPad = Int(body[0] & 0x07) + 2
        for _ in 0..<headerPad { body.append(UInt8.random(in: 0...255)) }
        body.append(plain)
        body.append(contentsOf: [0, 0, 0, 0, 0, 0, 0])
        while body.count % 8 != 0 { body.append(0) }
        return body
    }

    private static func unpad(_ data: Data) throws -> Data {
        guard data.count >= 10 else { throw ProtocolError.tea("plaintext too short") }
        let headerPad = Int(data[0] & 0x07) + 3
        guard data.count >= headerPad + 7 else {
            throw ProtocolError.tea("QQ-TEA trailing zero validation failed at header")
        }
        let tail = data.suffix(7)
        guard tail.allSatisfy({ $0 == 0 }) else {
            throw ProtocolError.tea("QQ-TEA trailing zero validation failed at tail")
        }
        return data.subdata(in: headerPad..<(data.count - 7))
    }

    private static func readUInt32BE(_ data: Data, _ offset: Int) -> UInt32 {
        data.withUnsafeBytes { raw in
            UInt32(bigEndian: raw.loadUnaligned(fromByteOffset: offset, as: UInt32.self))
        }
    }

    private static func bytesBE(_ value: UInt32) -> [UInt8] {
        let be = value.bigEndian
        return withUnsafeBytes(of: be) { Array($0) }
    }
}

enum ProtocolError: LocalizedError {
    case tea(String)
    case framing(String)
    case server(String)
    case disconnected

    var errorDescription: String? {
        switch self {
        case .tea(let message), .framing(let message), .server(let message):
            return message
        case .disconnected:
            return "本地协议连接已断开"
        }
    }
}
