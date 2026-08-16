import Foundation

enum AppScreen: Equatable {
    case login
    case sectionSelect
    case lobby
    case room
    case battle
}

/// `uiSelSect.pyc` exposes five top-level channel tabs in this order.
enum SectionChannel: Int, CaseIterable, Identifiable {
    case practice
    case beginner
    case free
    case match
    case party

    var id: Int { rawValue }

    var assetName: String {
        switch self {
        case .practice: "practice"
        case .beginner: "greenhand"
        case .free: "freedom"
        case .match: "match"
        case .party: "party"
        }
    }

    var title: String {
        switch self {
        case .practice: "练习频道"
        case .beginner: "新手频道"
        case .free: "自由频道"
        case .match: "比赛频道"
        case .party: "探险频道"
        }
    }
}

struct ServerArea: Identifiable, Equatable {
    let id: Int
    let name: String
    let ping: Int
}

enum SectionPopulation: Int, Equatable {
    case open = 1
    case smooth = 2
    case busy = 3
    case crowded = 4
    case full = 5
    case maintenance = 6

    var title: String {
        switch self {
        case .open: "空闲"
        case .smooth: "流畅"
        case .busy: "繁忙"
        case .crowded: "拥挤"
        case .full: "爆满"
        case .maintenance: "维护"
        }
    }
}

struct GameSection: Identifiable, Equatable {
    let id: Int
    let name: String
    let channel: SectionChannel
    let areaID: Int
    let population: SectionPopulation
    let ping: Int
}

enum ChannelKind: String, CaseIterable, Identifiable {
    case casual = "休闲"
    case arena = "竞技"

    var id: String { rawValue }
}

enum RoomStatus: String, Equatable {
    case waiting
    case playing
}

enum TeamSide: Int, Equatable {
    case red = 0
    case blue = 1

    var title: String { self == .red ? "红队" : "蓝队" }
}

struct Account: Identifiable, Equatable {
    var uid: Int64
    var nickname: String
    var level: Int
    var characterId: String
    var adventureCards: Int
    var staminaPotions: Int

    var id: Int64 { uid }
}

struct Seat: Identifiable, Equatable {
    var index: Int
    var uid: Int64?
    var nickname: String?
    var team: TeamSide
    var isReady: Bool
    var isHost: Bool
    var characterCode: String = "boy"

    var id: Int { index }
    var isOccupied: Bool { uid != nil }
}

/// 角色名单逐字来自 `uiRoom.pyc`：
/// RoleNameList = (['random','boy','girl','xwk','tt','xq','cl','bbl',
///                  'hy','mly','hwz','yd','ld','ge','tb'], ['kl','nz','wll','yy'])
/// 行走图来自存档站 ui/player/n{XX}，编号按原版名单顺序对应。
enum RoleCatalog {
    static let normalRoster: [String] = [
        "random", "boy", "girl", "xwk", "tt", "xq", "cl", "bbl",
        "hy", "mly", "hwz", "yd", "ld", "ge", "tb",
    ]

    static let vipRoster: [String] = ["kl", "nz", "wll", "yy"]

    static var allSelectable: [String] { normalRoster + vipRoster }

    private static let spriteIndex: [String: Int] = [
        "boy": 2, "girl": 3, "xwk": 4, "tt": 5, "xq": 6, "cl": 7,
        "bbl": 8, "hy": 9, "mly": 10, "hwz": 11, "yd": 12, "ld": 13,
        "ge": 14, "tb": 15, "kl": 16, "nz": 17, "wll": 18, "yy": 19,
    ]

    static func spriteCode(for role: String) -> String? {
        spriteIndex[role].map { String(format: "n%02d", $0) }
    }

    static func resolveRandom(_ role: String) -> String {
        role == "random" ? (normalRoster.dropFirst().randomElement() ?? "boy") : role
    }

    /// 座位/战斗行走图路径；c1=红队配色，c2=蓝队配色（仅站立帧）。
    static func standSprite(for role: String, team: TeamSide) -> String? {
        guard let sprite = spriteCode(for: role) else { return nil }
        let colored = "Game/Player/\(sprite)-c\(team == .blue ? 2 : 1)-down-1.png"
        if Legacy43Assets.image(colored) != nil { return colored }
        let fallback = "Game/Player/\(sprite)-c1-down-1.png"
        return Legacy43Assets.image(fallback) != nil ? fallback : nil
    }
}

/// 一名进入战斗的玩家（本地玩家 + 房间座位上的其他人）。
struct BattleParticipant: Equatable {
    var uid: Int64
    var name: String
    var team: TeamSide
    var spriteCode: String
    var isLocal: Bool
}

struct GameRoom: Identifiable, Equatable {
    var id: Int
    var name: String
    var mapId: Int
    var mapFile: String
    var mapName: String
    var channel: ChannelKind
    var status: RoomStatus
    var seats: [Seat]

    var occupiedCount: Int { seats.filter(\.isOccupied).count }
    var capacity: Int { seats.count }

    var hostSeat: Seat? { seats.first(where: \.isHost) }

    func seat(for uid: Int64) -> Seat? {
        seats.first { $0.uid == uid }
    }
}

struct ChatLine: Identifiable, Equatable {
    let id: UUID
    let text: String
    let createdAt: Date

    init(text: String) {
        self.id = UUID()
        self.text = text
        self.createdAt = Date()
    }
}

struct BattleMap: Equatable {
    var legacyMapId: Int
    var name: String
    var width: Int
    var height: Int
    var mapFile: String
    var previewFile: String
    var blocked: Set<GridPoint>
    var spawnPoints: [GridPoint]
    var destructible: Set<GridPoint> = []
    var elements: [BattleMapElement] = []
    var mapVersion: Int = 0
    var gameMode: Int = 0
    var maxPlayers: Int = 0
}

struct GridPoint: Hashable, Equatable {
    var x: Int
    var y: Int
}

struct BattleMapElement: Equatable {
    var id: Int
    /// Original file layer: 0 = foreground, 1 = objects, 2 = ground.
    var layer: Int
    var origin: GridPoint
    var width: Int
    var height: Int
    var xOffset: Int
    var yOffset: Int
    var life: Int
    var attributes: [UInt32]
    var isDestructible: Bool

    var occupiedPoints: [GridPoint] {
        (0..<height).flatMap { row in
            (0..<width).map { column in
                GridPoint(x: origin.x + column, y: origin.y + row)
            }
        }
    }
}
