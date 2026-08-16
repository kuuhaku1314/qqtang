import AppKit
import Foundation

final class LegacyAssetStore {
    static let shared = LegacyAssetStore()

    private var cache: [String: NSImage] = [:]
    private var mapElementCache: [String: NSImage] = [:]

    func nsImage(relativePath: String) -> NSImage? {
        if let cached = cache[relativePath] { return cached }
        guard let decoded = try? QQFDIMGImage.load(from: GameDataLocator.url(relativePath: relativePath)) else {
            return nil
        }
        cache[relativePath] = decoded.nsImage
        return decoded.nsImage
    }

    func loadLoginBackground() -> NSImage? {
        nsImage(relativePath: "ui/selSect/bg_login.img")
    }

    func loadLoginLogo() -> NSImage? {
        nsImage(relativePath: "object/ui/login/img_logo.img")
    }

    func loadMapImage(for entry: LegacyMapEntry) -> NSImage? {
        nsImage(relativePath: "map/\(entry.previewFile)")
    }

    func loadMapTypeIcon(_ type: String, large: Bool = true) -> NSImage? {
        let folder = large ? "bigIcon" : "icon"
        return nsImage(relativePath: "map/\(folder)/\(type).img")
            ?? nsImage(relativePath: "map/\(folder)/rand.img")
            ?? nsImage(relativePath: "map/\(folder)/water.img")
    }

    func loadMapElementImage(id: Int, state: String = "stand") -> NSImage? {
        guard let city = mapCityFolder(for: id) else { return nil }
        let fileName = "elem\(id % 1000)_\(state).img"
        let cacheKey = "\(city)/\(fileName)"
        if let cached = mapElementCache[cacheKey] {
            return cached
        }

        let gameDataCandidates = [
            "object/mapElem/\(city)/\(fileName)",
            "object/\(city)/\(fileName)",
        ]
        for relativePath in gameDataCandidates {
            if let url = try? GameDataLocator.url(relativePath: relativePath),
               let decoded = try? QQFDIMGImage.load(from: url)
            {
                mapElementCache[cacheKey] = decoded.nsImage
                return decoded.nsImage
            }
        }

        let bundledPath = "Game/MapElements/\(city)/\(fileName)"
        if let url = Legacy43Assets.fileURL(bundledPath),
           let decoded = try? QQFDIMGImage.load(from: url)
        {
            mapElementCache[cacheKey] = decoded.nsImage
            return decoded.nsImage
        }
        return nil
    }

    private func mapCityFolder(for id: Int) -> String? {
        switch id / 1000 {
        case 1: "desert"
        case 2: "snow"
        case 3: "town"
        case 4: "mine"
        case 5: "water"
        case 6: "field"
        case 7: "bomb"
        case 8: "bun"
        case 9: "pig"
        case 10: "treasure"
        case 11: "match"
        case 12: "sculpture"
        case 13: "machine"
        case 14: "box"
        case 15: "practice"
        case 16: "exploration"
        case 17: "common"
        case 18: "pve"
        case 19: "tank"
        default: nil
        }
    }
}
