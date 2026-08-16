import Foundation

struct ProtocolInventoryItem: Codable, Equatable {
    var id: Int
    var quantity: Int
}

struct ProtocolGameInfo: Codable, Equatable {
    var wins: Int
    var losses: Int
    var draws: Int
    var points: Int
    var money: Int
    var degree: Int
    var roleId: Int

    enum CodingKeys: String, CodingKey {
        case wins, losses, draws, points, money, degree
        case roleId = "role_id"
    }
}

struct ProtocolPlayerProfile: Codable, Equatable {
    var playerId: Int
    var gameInfo: ProtocolGameInfo
    var inventory: [ProtocolInventoryItem]
}

struct ProtocolListener: Codable, Equatable {
    var name: String
    var network: String
    var address: String
}

struct ProtocolConfig: Codable, Equatable {
    var seedUin: Int
    var databasePath: String
    var playerProfile: ProtocolPlayerProfile
    var listeners: [ProtocolListener]

    static func load() -> ProtocolConfig? {
        guard let catalogRoot = GameDataLocator.catalogRoot() else { return nil }
        let url = catalogRoot.appendingPathComponent("protocol-local-ui.json")
        guard let data = try? Data(contentsOf: url) else { return nil }
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try? decoder.decode(ProtocolConfig.self, from: data)
    }

    var directoryPort: Int? {
        listeners.first { $0.name == "directory-game-capture" }
            .flatMap { parsePort($0.address) }
    }

    var httpPort: Int? {
        listeners.first { $0.name == "directory-http-local-ui" }
            .flatMap { parsePort($0.address) }
    }

    private func parsePort(_ address: String) -> Int? {
        address.split(separator: ":").last.flatMap { Int($0) }
    }
}

enum ProtocolBridge {
    static func seedInventory() -> (adventureCards: Int, staminaPotions: Int, level: Int) {
        guard let config = ProtocolConfig.load() else {
            return (500, 500, 1)
        }
        let cards = config.playerProfile.inventory.first { $0.id == 99 }?.quantity ?? 500
        let potions = config.playerProfile.inventory.first { $0.id == 20043 }?.quantity ?? 500
        let level = max(1, config.playerProfile.gameInfo.degree)
        return (cards, potions, level)
    }
}
