import Foundation

/// Parsed representation of the original little-endian QQTang `.map` format.
struct QQMapLayout {
    var version: Int
    var gameMode: Int
    var maxPlayers: Int
    var width: Int
    var height: Int
    var blocked: Set<GridPoint>
    var destructible: Set<GridPoint>
    var spawnPoints: [GridPoint]
    var spawnGroups: [[GridPoint]]
    var itemPoints: Set<GridPoint>
    var specialPoints: [GridPoint]
    var elements: [BattleMapElement]

    static func load(entry: LegacyMapEntry) throws -> QQMapLayout {
        let mapURL = try GameDataLocator.url(relativePath: "map/\(entry.mapFile)")
        var reader = LittleEndianReader(data: try Data(contentsOf: mapURL))

        let version = Int(try reader.read(Int32.self))
        guard version == 3 || version == 4 else {
            throw LegacyAssetError.invalidFormat("不支持的地图版本：\(version)")
        }

        let gameMode = Int(try reader.read(Int32.self))
        let maxPlayers = Int(try reader.read(Int32.self))
        let width: Int
        let height: Int
        if version == 3 {
            width = 15
            height = 13
        } else {
            width = Int(try reader.read(Int32.self))
            height = Int(try reader.read(Int32.self))
        }
        guard (1...128).contains(width), (1...128).contains(height) else {
            throw LegacyAssetError.invalidFormat("地图尺寸无效：\(width)×\(height)")
        }

        let cellCount = width * height
        var layers = [[Int32]]()
        layers.reserveCapacity(3)
        for _ in 0..<3 {
            var cells = [Int32]()
            cells.reserveCapacity(cellCount)
            for _ in 0..<cellCount {
                cells.append(try reader.read(Int32.self))
            }
            layers.append(cells)
        }

        // Old item-weight table: item id, two integer fields and a Float32.
        let itemDefinitionCount = try reader.readCount("道具定义", maximum: 16_384)
        try reader.skip(itemDefinitionCount * 16)

        let itemPointCount = try reader.readCount("道具生成点", maximum: cellCount)
        var itemPoints = Set<GridPoint>()
        for _ in 0..<itemPointCount {
            itemPoints.insert(try reader.readGridPoint())
        }

        var spawnGroups = [[GridPoint]]()
        spawnGroups.reserveCapacity(2)
        for _ in 0..<2 {
            let count = try reader.readCount("出生点", maximum: 64)
            var group = [GridPoint]()
            group.reserveCapacity(count)
            for _ in 0..<count {
                group.append(try reader.readGridPoint())
            }
            spawnGroups.append(group)
        }

        let specialPointCount = try reader.readCount("特殊点", maximum: 64)
        var specialPoints = [GridPoint]()
        specialPoints.reserveCapacity(specialPointCount)
        for _ in 0..<specialPointCount {
            specialPoints.append(try reader.readGridPoint())
        }

        let properties = try QQMapElementProperty.loadCatalog()
        var elements = [BattleMapElement]()
        var blocked = Set<GridPoint>()
        var destructible = Set<GridPoint>()

        for layer in 0..<layers.count {
            for y in 0..<height {
                for x in 0..<width {
                    let rawID = layers[layer][y * width + x]
                    guard rawID > 0 else { continue }

                    let id = Int(rawID)
                    let property = properties[id] ?? .fallback(id: id)
                    let origin = GridPoint(x: x, y: y)
                    let canBreak = itemPoints.contains(origin)
                        || (itemPoints.isEmpty && property.life > 0)
                    let element = BattleMapElement(
                        id: id,
                        layer: layer,
                        origin: origin,
                        width: property.width,
                        height: property.height,
                        xOffset: property.xOffset,
                        yOffset: property.yOffset,
                        life: property.life,
                        attributes: property.attributes,
                        isDestructible: canBreak
                    )
                    elements.append(element)

                    guard layer != 2 else { continue }
                    for localY in 0..<property.height {
                        for localX in 0..<property.width {
                            let point = GridPoint(x: x + localX, y: y + localY)
                            guard point.x >= 0, point.y >= 0,
                                  point.x < width, point.y < height
                            else { continue }

                            let attributeIndex = localY * property.width + localX
                            let attribute = property.attributes.indices.contains(attributeIndex)
                                ? property.attributes[attributeIndex]
                                : 0
                            // The low nibble contains the four directional player-pass bits.
                            guard attribute & 0xF != 0xF else { continue }
                            if canBreak {
                                destructible.insert(point)
                            } else {
                                blocked.insert(point)
                            }
                        }
                    }
                }
            }
        }

        return QQMapLayout(
            version: version,
            gameMode: gameMode,
            maxPlayers: maxPlayers,
            width: width,
            height: height,
            blocked: blocked,
            destructible: destructible,
            spawnPoints: spawnGroups.flatMap { $0 },
            spawnGroups: spawnGroups,
            itemPoints: itemPoints,
            specialPoints: specialPoints,
            elements: elements
        )
    }
}

private struct QQMapElementProperty {
    let id: Int
    let width: Int
    let height: Int
    let xOffset: Int
    let yOffset: Int
    let life: Int
    let attributes: [UInt32]

    static func fallback(id: Int) -> QQMapElementProperty {
        QQMapElementProperty(
            id: id,
            width: 1,
            height: 1,
            xOffset: 0,
            yOffset: 0,
            life: -1,
            attributes: [0]
        )
    }

    static func loadCatalog() throws -> [Int: QQMapElementProperty] {
        let url = try GameDataLocator.url(relativePath: "object/mapElem/mapElem.prop")
        var reader = LittleEndianReader(data: try Data(contentsOf: url))
        _ = try reader.read(Int32.self) // property format version
        let count = try reader.readCount("地图元素", maximum: 100_000)
        var result = [Int: QQMapElementProperty](minimumCapacity: count)

        for _ in 0..<count {
            let id = Int(try reader.read(Int32.self))
            let width = Int(try reader.read(Int16.self))
            let height = Int(try reader.read(Int16.self))
            let xOffset = Int(try reader.read(Int16.self))
            let yOffset = Int(try reader.read(Int16.self))
            let life = Int(try reader.read(Int32.self))
            _ = try reader.read(Int32.self) // level
            _ = try reader.read(UInt32.self) // movement/special flags

            guard (1...128).contains(width), (1...128).contains(height),
                  width * height <= 16_384
            else {
                throw LegacyAssetError.invalidFormat("地图元素 \(id) 尺寸无效")
            }

            var attributes = [UInt32]()
            attributes.reserveCapacity(width * height)
            for _ in 0..<(width * height) {
                attributes.append(try reader.read(UInt32.self))
            }
            result[id] = QQMapElementProperty(
                id: id,
                width: width,
                height: height,
                xOffset: xOffset,
                yOffset: yOffset,
                life: life,
                attributes: attributes
            )
        }
        return result
    }
}

private struct LittleEndianReader {
    let data: Data
    private(set) var offset = 0

    mutating func read<T: FixedWidthInteger>(_ type: T.Type) throws -> T {
        let byteCount = MemoryLayout<T>.size
        guard offset <= data.count - byteCount else {
            throw LegacyAssetError.invalidFormat("地图文件在偏移 \(offset) 处截断")
        }
        let value = data.withUnsafeBytes {
            $0.loadUnaligned(fromByteOffset: offset, as: T.self)
        }
        offset += byteCount
        return T(littleEndian: value)
    }

    mutating func skip(_ byteCount: Int) throws {
        guard byteCount >= 0, offset <= data.count - byteCount else {
            throw LegacyAssetError.invalidFormat("地图文件跳过字段时截断")
        }
        offset += byteCount
    }

    mutating func readCount(_ label: String, maximum: Int) throws -> Int {
        let value = Int(try read(Int32.self))
        guard value >= 0, value <= maximum else {
            throw LegacyAssetError.invalidFormat("\(label)数量无效：\(value)")
        }
        return value
    }

    /// The file stores each coordinate as row first, then column.
    mutating func readGridPoint() throws -> GridPoint {
        let row = Int(try read(Int16.self))
        let column = Int(try read(Int16.self))
        return GridPoint(x: column, y: row)
    }
}
