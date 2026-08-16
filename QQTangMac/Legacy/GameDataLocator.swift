import Foundation

enum GameDataLocator {
    static func clientRoot() -> URL? {
        candidates().first { FileManager.default.fileExists(atPath: $0.path) }
    }

    static func catalogRoot() -> URL? {
        let bundleCatalog = Bundle.main.resourceURL?.appendingPathComponent("catalog", isDirectory: true)
        let roots = [
            ProcessInfo.processInfo.environment["QQTANG_CATALOG"],
            bundleCatalog?.path,
            repoCatalogPath()?.path,
            supportCatalogPath().path
        ].compactMap { $0 }

        return roots
            .map { URL(fileURLWithPath: $0, isDirectory: true) }
            .first { FileManager.default.fileExists(atPath: $0.path) }
    }

    static func url(relativePath: String) throws -> URL {
        guard let root = clientRoot() else { throw LegacyAssetError.missingRoot }
        let url = root.appendingPathComponent(relativePath)
        guard FileManager.default.fileExists(atPath: url.path) else {
            throw LegacyAssetError.missingFile(relativePath)
        }
        return url
    }

    static func statusLine() -> String {
        if let root = clientRoot() {
            return "原版资源：\(root.path)"
        }
        return "原版资源未导入（运行 ./scripts/import-gamedata.sh）"
    }

    private static func candidates() -> [URL] {
        [
            ProcessInfo.processInfo.environment["QQTANG_GAMEDATA"].map {
                URL(fileURLWithPath: $0, isDirectory: true)
            },
            repoClientPath(),
            URL(fileURLWithPath: "/Users/yang/Downloads/qqtmac/GameData/client-patched", isDirectory: true),
            supportClientPath()
        ].compactMap { $0 }
    }

    private static func repoClientPath() -> URL? {
        let source = URL(fileURLWithPath: #filePath)
        var directory = source.deletingLastPathComponent() // Legacy
        directory.deleteLastPathComponent() // QQTangMac
        directory.deleteLastPathComponent() // repo root
        return directory.appendingPathComponent("GameData/client-patched", isDirectory: true)
    }

    private static func repoCatalogPath() -> URL? {
        repoClientPath()?.deletingLastPathComponent().appendingPathComponent("catalog", isDirectory: true)
    }

    private static func supportClientPath() -> URL {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return support.appendingPathComponent("QQTangMac/client-patched", isDirectory: true)
    }

    private static func supportCatalogPath() -> URL {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return support.appendingPathComponent("QQTangMac/catalog", isDirectory: true)
    }
}
