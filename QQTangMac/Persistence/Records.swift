import Foundation
import GRDB

struct AccountRecord: Codable, FetchableRecord, PersistableRecord, Equatable {
    static let databaseTableName = "accounts"

    var uid: Int64
    var nickname: String
    var level: Int
    var characterId: String
    var adventureCards: Int
    var staminaPotions: Int

    func toAccount() -> Account {
        Account(
            uid: uid,
            nickname: nickname,
            level: level,
            characterId: characterId,
            adventureCards: adventureCards,
            staminaPotions: staminaPotions
        )
    }

    static func from(_ account: Account) -> AccountRecord {
        AccountRecord(
            uid: account.uid,
            nickname: account.nickname,
            level: account.level,
            characterId: account.characterId,
            adventureCards: account.adventureCards,
            staminaPotions: account.staminaPotions
        )
    }
}

enum SeedAccount {
    static let uid: Int64 = 1_000_001
    static let nickname = "堂主"
    static let characterId = "default"

    static func makeRecord() -> AccountRecord {
        let seeded = ProtocolBridge.seedInventory()
        return AccountRecord(
            uid: uid,
            nickname: nickname,
            level: seeded.level,
            characterId: characterId,
            adventureCards: seeded.adventureCards,
            staminaPotions: seeded.staminaPotions
        )
    }
}
