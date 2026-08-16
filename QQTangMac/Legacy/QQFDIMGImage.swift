import AppKit
import Foundation

/// QQF/DIMG decoder, layout verified against the original 4.3 client
/// (and qqt_map_editor_fin `QQFDIMG.cpp`):
///
///     0x00  "QQF\x1a" "DIMG"
///     0x08  i16 version   (0 = RGB565 + alpha 0...32, 1 = BGRA8888)
///     0x10  u32 frame count, 0x14 u32 direction count
///     0x18  i32 x offset,   0x1c i32 y offset
///     0x20  i32 canvas width, 0x24 i32 canvas height
///     per frame: i32 skip, i32 frameX, i32 frameY, i32 skip,
///                i32 width, i32 height, i32 skip, pixel planes
struct QQFDIMGImage {
    let width: Int
    let height: Int
    let cgImage: CGImage

    static func load(from url: URL, frame: Int = 0) throws -> QQFDIMGImage {
        try decode(try Data(contentsOf: url), frame: frame)
    }

    static func decode(_ data: Data, frame requested: Int = 0) throws -> QQFDIMGImage {
        guard data.count >= 0x28 + 28,
              data[0] == 0x51, data[1] == 0x51, data[2] == 0x46, data[3] == 0x1A,
              String(data: data[4..<8], encoding: .ascii) == "DIMG"
        else {
            throw LegacyAssetError.invalidFormat("不是 QQF/DIMG 文件")
        }

        let version = Int(readInt16(data, 0x08))
        guard version == 0 || version == 1 else {
            throw LegacyAssetError.invalidFormat("未支持的 DIMG 版本 \(version)")
        }
        let frameCount = max(1, Int(readUInt32(data, 0x10)))
        let target = min(max(requested, 0), frameCount - 1)

        var offset = 0x28
        for index in 0..<frameCount {
            guard data.count >= offset + 28 else {
                throw LegacyAssetError.invalidFormat("帧头越界")
            }
            let width = Int(readInt32(data, offset + 16))
            let height = Int(readInt32(data, offset + 20))
            guard (1...4096).contains(width), (1...4096).contains(height) else {
                throw LegacyAssetError.invalidFormat("帧尺寸无效 \(width)x\(height)")
            }
            let pixelOffset = offset + 28
            let pixelBytes = width * height * (version == 0 ? 3 : 4)
            guard data.count >= pixelOffset + pixelBytes else {
                throw LegacyAssetError.invalidFormat("像素数据不完整")
            }
            if index == target {
                return try decodeFrame(
                    data, version: version,
                    pixelOffset: pixelOffset, width: width, height: height
                )
            }
            offset = pixelOffset + pixelBytes
        }
        throw LegacyAssetError.invalidFormat("找不到请求的帧")
    }

    private static func decodeFrame(
        _ data: Data, version: Int, pixelOffset: Int, width: Int, height: Int
    ) throws -> QQFDIMGImage {
        var rgba = [UInt8](repeating: 0, count: width * height * 4)
        let count = width * height
        if version == 0 {
            let alphaOffset = pixelOffset + count * 2
            for i in 0..<count {
                let value = UInt16(data[pixelOffset + i * 2])
                    | (UInt16(data[pixelOffset + i * 2 + 1]) << 8)
                let alpha = UInt8(min(Int(data[alphaOffset + i]) * 8, 255))
                let (red, green, blue) = rgb565(value)
                write(&rgba, i, red, green, blue, alpha)
            }
        } else {
            for i in 0..<count {
                let base = pixelOffset + i * 4
                write(&rgba, i, data[base + 2], data[base + 1], data[base], data[base + 3])
            }
        }

        guard let provider = CGDataProvider(data: Data(rgba) as CFData),
              let image = CGImage(
                width: width,
                height: height,
                bitsPerComponent: 8,
                bitsPerPixel: 32,
                bytesPerRow: width * 4,
                space: CGColorSpaceCreateDeviceRGB(),
                bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedLast.rawValue),
                provider: provider,
                decode: nil,
                shouldInterpolate: true,
                intent: .defaultIntent
              )
        else {
            throw LegacyAssetError.invalidFormat("无法创建 CGImage")
        }
        return QQFDIMGImage(width: width, height: height, cgImage: image)
    }

    private static func write(
        _ rgba: inout [UInt8], _ index: Int,
        _ red: UInt8, _ green: UInt8, _ blue: UInt8, _ alpha: UInt8
    ) {
        let base = index * 4
        if alpha == 255 {
            rgba[base] = red
            rgba[base + 1] = green
            rgba[base + 2] = blue
        } else {
            rgba[base] = UInt8(UInt16(red) * UInt16(alpha) / 255)
            rgba[base + 1] = UInt8(UInt16(green) * UInt16(alpha) / 255)
            rgba[base + 2] = UInt8(UInt16(blue) * UInt16(alpha) / 255)
        }
        rgba[base + 3] = alpha
    }

    var nsImage: NSImage {
        NSImage(cgImage: cgImage, size: NSSize(width: width, height: height))
    }

    private static func rgb565(_ value: UInt16) -> (UInt8, UInt8, UInt8) {
        let red = UInt8(((value >> 11) & 0x1F) * 255 / 31)
        let green = UInt8(((value >> 5) & 0x3F) * 255 / 63)
        let blue = UInt8((value & 0x1F) * 255 / 31)
        return (red, green, blue)
    }

    private static func readUInt32(_ data: Data, _ offset: Int) -> UInt32 {
        data.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: offset, as: UInt32.self) }
    }

    private static func readInt32(_ data: Data, _ offset: Int) -> Int32 {
        data.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: offset, as: Int32.self) }
    }

    private static func readInt16(_ data: Data, _ offset: Int) -> Int16 {
        data.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: offset, as: Int16.self) }
    }
}

enum LegacyAssetError: LocalizedError {
    case missingRoot
    case missingFile(String)
    case invalidFormat(String)

    var errorDescription: String? {
        switch self {
        case .missingRoot:
            return "未找到 GameData/client-patched，请先运行 ./scripts/import-gamedata.sh"
        case .missingFile(let path):
            return "缺少资源文件：\(path)"
        case .invalidFormat(let message):
            return message
        }
    }
}
