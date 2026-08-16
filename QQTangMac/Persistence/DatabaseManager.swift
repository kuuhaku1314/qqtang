import Foundation
import GRDB

enum DatabaseError: LocalizedError {
    case notFound
    case io(String)

    var errorDescription: String? {
        switch self {
        case .notFound:
            return "本地账号不存在"
        case .io(let message):
            return message
        }
    }
}

final class DatabaseManager {
    static let shared = DatabaseManager()

    let dbQueue: DatabaseQueue
    let databaseURL: URL

    init() {
        do {
            let support = try FileManager.default.url(
                for: .applicationSupportDirectory,
                in: .userDomainMask,
                appropriateFor: nil,
                create: true
            )
            let folder = support.appendingPathComponent("QQTangMac", isDirectory: true)
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            databaseURL = folder.appendingPathComponent("qqtang.sqlite")
            dbQueue = try DatabaseQueue(path: databaseURL.path)
            try migrator.migrate(dbQueue)
            try seedIfNeeded()
        } catch {
            fatalError("无法初始化本地存档：\(error)")
        }
    }

    private var migrator: DatabaseMigrator {
        var migrator = DatabaseMigrator()
        migrator.registerMigration("v1_accounts") { db in
            try db.create(table: AccountRecord.databaseTableName) { table in
                table.column("uid", .integer).primaryKey()
                table.column("nickname", .text).notNull()
                table.column("level", .integer).notNull().defaults(to: 1)
                table.column("characterId", .text).notNull()
                table.column("adventureCards", .integer).notNull().defaults(to: 0)
                table.column("staminaPotions", .integer).notNull().defaults(to: 0)
            }
        }
        return migrator
    }

    private func seedIfNeeded() throws {
        try dbQueue.write { db in
            if try AccountRecord.fetchOne(db, key: SeedAccount.uid) == nil {
                try SeedAccount.makeRecord().insert(db)
            }
        }
    }

    func fetchAccount(uid: Int64) throws -> Account {
        try dbQueue.read { db in
            guard let record = try AccountRecord.fetchOne(db, key: uid) else {
                throw DatabaseError.notFound
            }
            return record.toAccount()
        }
    }

    func save(_ account: Account) throws {
        try dbQueue.write { db in
            try AccountRecord.from(account).save(db)
        }
    }
}
