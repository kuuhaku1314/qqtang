import Foundation

struct LegacyMapEntry: Identifiable, Codable, Equatable {
    var id: Int
    var type: String
    var name: String
    var width: Int
    var height: Int
    var maxPlayers: Int
    var mapFile: String
    var previewFile: String
    var bgmFile: String

    var stem: String {
        mapFile.replacingOccurrences(of: ".map", with: "")
    }
}

struct MapCatalogFile: Codable {
    var schemaVersion: Int
    var maps: [LegacyMapEntry]
}

enum MapCatalog {
    static func load() -> [LegacyMapEntry] {
        guard let catalogRoot = GameDataLocator.catalogRoot() else { return fallback }
        let url = catalogRoot.appendingPathComponent("maps.json")
        guard let data = try? Data(contentsOf: url),
              let file = try? JSONDecoder().decode(MapCatalogFile.self, from: data)
        else { return fallback }
        return file.maps
    }

    static func entry(mapFile: String) -> LegacyMapEntry? {
        load().first { $0.mapFile == mapFile || $0.stem == mapFile }
    }

    static func entry(id: Int) -> LegacyMapEntry? {
        load().first { $0.id == id }
    }

    static var defaultEntry: LegacyMapEntry {
        entry(mapFile: "pig01_4.map")
            ?? entry(id: 901)
            ?? entry(mapFile: "match01_2.map")
            ?? fallback[0]
    }

    private static let fallback: [LegacyMapEntry] = [
        LegacyMapEntry(
            id: 901,
            type: "pig",
            name: "功夫01",
            width: 15,
            height: 13,
            maxPlayers: 4,
            mapFile: "pig01_4.map",
            previewFile: "pig01_4.img",
            bgmFile: "match.ogg"
        ),
        LegacyMapEntry(
            id: 1001,
            type: "match",
            name: "比武01",
            width: 15,
            height: 13,
            maxPlayers: 2,
            mapFile: "match01_2.map",
            previewFile: "match01_2.img",
            bgmFile: "M15.OGG"
        ),
        LegacyMapEntry(
            id: 201,
            type: "town",
            name: "中国城01",
            width: 15,
            height: 13,
            maxPlayers: 4,
            mapFile: "town01_4.map",
            previewFile: "town01_4.img",
            bgmFile: "town.ogg"
        )
    ]
}
