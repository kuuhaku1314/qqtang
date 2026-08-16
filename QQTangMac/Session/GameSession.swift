import AppKit
import Foundation
import SwiftUI

@MainActor
final class GameSession: ObservableObject {
    @Published private(set) var screen: AppScreen = .login
    @Published var loginUID: String = "\(SeedAccount.uid)"
    // 原版本地版行为：GetLoginUinFromCmdLn 预填 uin，密码显示 ********
    @Published var loginPassword: String = "********"
    @Published var loginError: String?
    @Published var isLoggingIn = false
    @Published private(set) var account: Account?
    @Published var selectedSectionChannel: SectionChannel = .beginner
    @Published var selectedAreaID: Int = 0
    @Published private(set) var selectedSection: GameSection?
    @Published private(set) var serverAreas: [ServerArea] = GameSession.seedServerAreas()
    @Published private(set) var sections: [GameSection] = GameSession.seedSections()
    @Published var selectedChannel: ChannelKind = .casual
    @Published private(set) var rooms: [GameRoom] = []
    @Published private(set) var currentRoom: GameRoom?
    @Published private(set) var chatLines: [ChatLine] = []
    @Published var createRoomName: String = "新手练习房"
    @Published var createRoomMapId: Int = MapCatalog.defaultEntry.id
    @Published private(set) var selectedRoleCode = "boy"
    @Published private(set) var battleSpriteCode = "n02"
    @Published private(set) var lastBombLog: String?
    @Published private(set) var assetStatus: String = GameDataLocator.statusLine()
    @Published private(set) var protocolStatus: String = "协议：正在启动本地服"

    let database: DatabaseManager
    private let assets = LegacyAssetStore.shared
    private let client = ProtocolClient()
    private var connected = false
    private var loginAttempt = UUID()

    var mapChoices: [LegacyMapEntry] {
        MapCatalog.load().filter { $0.maxPlayers >= 2 }
    }

    var mapCatalog: [LegacyMapEntry] {
        mapChoices
    }

    var currentMap: LegacyMapEntry? {
        guard let mapID = currentRoom?.mapId else { return nil }
        return map(for: mapID)
    }

    var currentUserSeat: Seat? {
        guard let uid = account?.uid else { return nil }
        return currentRoom?.seat(for: uid)
    }

    var isCurrentUserHost: Bool {
        currentUserSeat?.isHost == true
    }

    var isCurrentUserReady: Bool {
        currentUserSeat?.isReady == true
    }

    var canHostStart: Bool {
        guard isCurrentUserHost, let room = currentRoom else { return false }
        return room.seats
            .filter { $0.isOccupied && !$0.isHost }
            .allSatisfy(\.isReady)
    }

    var visibleSections: [GameSection] {
        sections.filter {
            $0.channel == selectedSectionChannel && $0.areaID == selectedAreaID
        }
    }

    var selectedArea: ServerArea? {
        serverAreas.first { $0.id == selectedAreaID }
    }

    init(database: DatabaseManager = .shared) {
        self.database = database
        LocalRuntime.shared.startIfNeeded()
        rooms = Self.seedRooms()
        refreshLegacyStatus()
    }

    var saveFilePath: String {
        database.databaseURL.path
    }

    func refreshLegacyStatus() {
        assetStatus = GameDataLocator.statusLine()
        protocolStatus = LocalRuntime.shared.status
    }

    func prepareSnapshot(screen target: AppScreen) {
        account = Account(
            uid: SeedAccount.uid,
            nickname: SeedAccount.nickname,
            level: 12,
            characterId: "n02",
            adventureCards: 3,
            staminaPotions: 2
        )
        selectedAreaID = 0
        selectedSectionChannel = .beginner
        selectedSection = sections.first {
            $0.areaID == selectedAreaID && $0.channel == selectedSectionChannel
        }
        rooms = Self.seedRooms()

        if target == .room || target == .battle {
            let map = MapCatalog.entry(id: 901) ?? MapCatalog.defaultEntry
            var room = Self.makeEmptyRoom(
                id: 188,
                name: "功夫01 新手练习",
                channel: .casual,
                map: map
            )
            sitLocal(
                &room,
                uid: SeedAccount.uid,
                nickname: SeedAccount.nickname,
                asHost: true
            )
            room.seats[1].uid = 900_001
            room.seats[1].nickname = "糖糖"
            room.seats[1].team = .blue
            room.seats[1].isReady = true
            room.seats[1].characterCode = "girl"
            if target == .battle {
                room.status = .playing
            }
            currentRoom = room
            chatLines = [
                ChatLine(text: "系统：欢迎来到 \(room.name)"),
                ChatLine(text: "糖糖：房主快开始吧！"),
            ]
        } else {
            currentRoom = nil
        }
        screen = target
    }

    func loginBackgroundImage() -> NSImage? {
        assets.loadLoginBackground()
    }

    func loginLogoImage() -> NSImage? {
        assets.loadLoginLogo()
    }

    /// 战斗参战名单：房间所有落座玩家，随机角色在此掷出。
    func battleParticipants() -> [BattleParticipant] {
        guard let room = currentRoom else { return [] }
        return room.seats.compactMap { seat in
            guard let uid = seat.uid else { return nil }
            let isLocal = uid == account?.uid
            let sprite: String
            if isLocal {
                sprite = battleSpriteCode
            } else {
                let role = RoleCatalog.resolveRandom(seat.characterCode)
                sprite = RoleCatalog.spriteCode(for: role) ?? "n02"
            }
            return BattleParticipant(
                uid: uid,
                name: seat.nickname ?? "玩家",
                team: seat.team,
                spriteCode: sprite,
                isLocal: isLocal
            )
        }
    }

    func battleMap(for room: GameRoom?) -> BattleMap {
        let entry = MapCatalog.entry(id: room?.mapId ?? MapCatalog.defaultEntry.id) ?? MapCatalog.defaultEntry
        let layout = (try? QQMapLayout.load(entry: entry))
            ?? QQMapLayout(
                version: 0,
                gameMode: 0,
                maxPlayers: entry.maxPlayers,
                width: entry.width,
                height: entry.height,
                blocked: [],
                destructible: [],
                spawnPoints: [GridPoint(x: 1, y: 1)],
                spawnGroups: [[GridPoint(x: 1, y: 1)], []],
                itemPoints: [],
                specialPoints: [],
                elements: []
            )
        return BattleMap(
            legacyMapId: entry.id,
            name: entry.name,
            width: layout.width,
            height: layout.height,
            mapFile: entry.mapFile,
            previewFile: entry.previewFile,
            blocked: layout.blocked,
            spawnPoints: layout.spawnPoints,
            destructible: layout.destructible,
            elements: layout.elements,
            mapVersion: layout.version,
            gameMode: layout.gameMode,
            maxPlayers: layout.maxPlayers
        )
    }

    func mapPreviewImage(mapId: Int) -> NSImage? {
        guard let entry = MapCatalog.entry(id: mapId) else { return nil }
        return mapPreviewImage(for: entry)
    }

    func map(for id: Int) -> LegacyMapEntry? {
        MapCatalog.entry(id: id)
    }

    func mapPreviewImage(for entry: LegacyMapEntry) -> NSImage? {
        if let image = assets.loadMapImage(for: entry) {
            return image
        }
        return Legacy43Assets.image(
            "Map/\(entry.previewFile.replacingOccurrences(of: ".img", with: "")).png"
        )
    }

    func login() {
        loginError = nil
        let trimmed = loginUID.trimmingCharacters(in: .whitespacesAndNewlines)
        // 原版 confirmBtn.OnClick 的校验与文案：uin < 10001 拒绝、密码非空
        guard let uid = Int64(trimmed), uid >= 10001 else {
            loginError = "您输入了无效的QQ号"
            return
        }
        guard !loginPassword.isEmpty else {
            loginError = "请输入密码"
            return
        }
        isLoggingIn = true
        let attempt = UUID()
        loginAttempt = attempt
        Task {
            let fetched: Account
            do {
                fetched = try database.fetchAccount(uid: uid)
            } catch {
                isLoggingIn = false
                loginError = error.localizedDescription
                return
            }
            Task { await syncProtocolLogin(uid: uid) }
            // 原版点击登录后显示 wait 对话框的连接过场
            try? await Task.sleep(nanoseconds: 800_000_000)
            guard isLoggingIn, loginAttempt == attempt else { return }
            account = fetched
            rooms = Self.seedRooms()
            refreshLegacyStatus()
            protocolStatus = "已登录 \(uid)（本地存档）"
            selectedSection = nil
            screen = .sectionSelect
            isLoggingIn = false
        }
    }

    /// 原版 wait 对话框的取消按钮（LoginTimeout）
    func cancelLogin() {
        loginAttempt = UUID()
        isLoggingIn = false
    }

    func selectSectionChannel(_ channel: SectionChannel) {
        selectedSectionChannel = channel
    }

    func selectServerArea(_ area: ServerArea) {
        selectedAreaID = area.id
    }

    func enterSection(_ section: GameSection) {
        guard section.population != .full, section.population != .maintenance else {
            loginError = section.population == .full ? "该小区人数已满，请选择其他小区" : "该小区正在维护"
            return
        }
        selectedSection = section
        selectedChannel = section.channel == .match ? .arena : .casual
        chatLines = [
            ChatLine(text: "系统：欢迎进入 \(selectedArea?.name ?? "") \(section.name)")
        ]
        screen = .lobby
    }

    func quickJoinSection() {
        guard let section = visibleSections
            .filter({ $0.population != .full && $0.population != .maintenance })
            .min(by: { ($0.population.rawValue, $0.ping) < ($1.population.rawValue, $1.ping) })
        else {
            loginError = "当前没有可进入的小区"
            return
        }
        enterSection(section)
    }

    func returnToLogin() {
        selectedSection = nil
        account = nil
        screen = .login
    }

    func leaveLobby() {
        currentRoom = nil
        chatLines = []
        screen = .sectionSelect
    }

    func createRoom() {
        Task { await performCreateRoom() }
    }

    func quickJoinRoom() {
        guard let room = filteredRooms().first(where: {
            $0.status == .waiting && $0.occupiedCount < $0.capacity
        }) else {
            appendChat("当前没有可加入的房间")
            return
        }
        joinRoom(id: room.id)
    }

    func joinRoom(id: Int) {
        Task { await performJoinRoom(id: id) }
    }

    func leaveRoom() {
        Task { await performLeaveRoom() }
    }

    func toggleReady() {
        Task { await performToggleReady() }
    }

    /// 原版行为：准备状态下点击角色无效；选中即改变自己座位上的角色。
    func selectRole(_ code: String) {
        guard RoleCatalog.allSelectable.contains(code) else { return }
        if currentUserSeat?.isReady == true { return }
        selectedRoleCode = code
        guard let uid = account?.uid, var room = currentRoom,
              let index = room.seats.firstIndex(where: { $0.uid == uid })
        else { return }
        room.seats[index].characterCode = code
        currentRoom = room
        if let listIndex = rooms.firstIndex(where: { $0.id == room.id }) {
            rooms[listIndex] = room
        }
    }

    func selectCurrentRoomMap(_ mapID: Int) {
        guard let uid = account?.uid,
              var room = currentRoom,
              room.seat(for: uid)?.isHost == true,
              let map = MapCatalog.entry(id: mapID)
        else { return }
        room.mapId = map.id
        room.mapFile = map.mapFile
        room.mapName = map.name
        currentRoom = room
        createRoomMapId = map.id
        if let index = rooms.firstIndex(where: { $0.id == room.id }) {
            rooms[index] = room
        }
        appendChat("地图已切换为 \(map.name)")
    }

    func setMyTeam(_ team: TeamSide) {
        guard let uid = account?.uid,
              var room = currentRoom,
              let index = room.seats.firstIndex(where: { $0.uid == uid })
        else { return }
        room.seats[index].team = team
        currentRoom = room
    }

    func startMatch() {
        Task { await performStartMatch() }
    }

    func exitBattle() {
        Task { await performExitBattle() }
    }

    func logBombPlaceholder() {
        Task { await performBomb() }
    }

    func sendChat(_ message: String) {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        chatLines.append(ChatLine(text: "\(account?.nickname ?? "玩家")：\(trimmed)"))
        if chatLines.count > 40 {
            chatLines.removeFirst(chatLines.count - 40)
        }
    }

    func filteredRooms() -> [GameRoom] {
        rooms.filter { $0.channel == selectedChannel }
    }

    private func ensureConnected() async throws {
        if connected { return }
        try await client.connect(port: UInt16(LocalRuntime.shared.gamePort))
        connected = true
    }

    private func syncProtocolLogin(uid: Int64) async {
        do {
            try await ensureConnected()
            let (success, snapshots) = try await client.login(uin: uid)
            if !snapshots.isEmpty {
                mergeRooms(snapshots.map(Self.makeRoom))
            }
            protocolStatus = "已登录 \(success.uin) · TEA 会话 · 127.0.0.1:\(LocalRuntime.shared.gamePort)"
        } catch {
            protocolStatus = "本地已登录，协议稍后同步：\(error.localizedDescription)"
        }
    }

    private func performCreateRoom() async {
        guard let account else { return }
        let name = createRoomName.trimmingCharacters(in: .whitespacesAndNewlines)
        let roomName = name.isEmpty ? "\(account.nickname)的房间" : name
        if connected {
            do {
                let snapshot = try await client.createRoom(
                    name: roomName,
                    mapId: createRoomMapId,
                    channel: selectedChannel.rawValue
                )
                enter(Self.makeRoom(snapshot))
                return
            } catch {
                appendChat("协议建房失败，改用本地房间")
            }
        }
        let map = MapCatalog.entry(id: createRoomMapId) ?? MapCatalog.defaultEntry
        var room = Self.makeEmptyRoom(id: Int.random(in: 2000...9999), name: roomName, channel: selectedChannel, map: map)
        sitLocal(&room, uid: account.uid, nickname: account.nickname, asHost: true)
        enter(room)
    }

    private func performJoinRoom(id: Int) async {
        guard let account else { return }
        if var room = rooms.first(where: { $0.id == id }) {
            if room.status == .playing {
                appendChat("房间正在游戏中")
                return
            }
            if room.occupiedCount >= room.capacity, room.seat(for: account.uid) == nil {
                appendChat("房间已满")
                return
            }
            if room.seat(for: account.uid) == nil {
                sitLocal(&room, uid: account.uid, nickname: account.nickname, asHost: room.occupiedCount == 0)
            }
            // 本地房间即时进入；不再回头应用迟到的协议快照，
            // 否则离开后可能被拽回房间。
            enter(room)
            return
        }

        guard connected else {
            appendChat("找不到房间 \(id)")
            return
        }
        do {
            let snapshot = try await client.joinRoom(id: id)
            enter(Self.makeRoom(snapshot))
        } catch {
            appendChat("加入房间失败：\(error.localizedDescription)")
        }
    }

    private func performLeaveRoom() async {
        // 返回大厅绝不等待协议：先切界面，再后台同步房间列表。
        currentRoom = nil
        chatLines = []
        screen = .lobby
        if connected {
            if let snapshots = try? await client.leaveRoom() {
                mergeRooms(snapshots.map(Self.makeRoom))
            }
        }
    }

    private func performToggleReady() async {
        if let uid = account?.uid, var room = currentRoom,
           let index = room.seats.firstIndex(where: { $0.uid == uid })
        {
            room.seats[index].isReady.toggle()
            currentRoom = room
            if let listIndex = rooms.firstIndex(where: { $0.id == room.id }) {
                rooms[listIndex] = room
            }
        }
        if let seat = currentRoom?.seat(for: account?.uid ?? 0) {
            appendChat("\(seat.nickname ?? "玩家") \(seat.isReady ? "已准备" : "取消准备")")
        }
        if connected {
            _ = try? await client.toggleReady()
        }
    }

    private func performStartMatch() async {
        guard var room = currentRoom else { return }
        guard let uid = account?.uid,
              let localSeat = room.seat(for: uid),
              localSeat.isHost
        else {
            appendChat("只有房主可以开始游戏")
            return
        }
        let guests = room.seats.filter { $0.isOccupied && !$0.isHost }
        guard guests.allSatisfy(\.isReady) else {
            appendChat("请等待其他玩家准备")
            return
        }
        room.status = .playing
        currentRoom = room
        lastBombLog = nil
        // 随机角色在开战时掷出实际人物（原版行为）。
        let resolvedRole = RoleCatalog.resolveRandom(selectedRoleCode)
        battleSpriteCode = RoleCatalog.spriteCode(for: resolvedRole) ?? "n02"
        screen = .battle
        if connected {
            _ = try? await client.startGame()
        }
    }

    private func performExitBattle() async {
        // 与返回大厅一样：先离开战斗画面，协议同步放在后面。
        if var room = currentRoom {
            room.status = .waiting
            room.seats = room.seats.map { seat in
                var next = seat
                next.isReady = false
                return next
            }
            currentRoom = room
        }
        appendChat("对战结束，已回到房间")
        screen = currentRoom == nil ? .lobby : .room
        if connected {
            _ = try? await client.exitBattle()
        }
    }

    private func performBomb() async {
        let stamp = ISO8601DateFormatter().string(from: Date())
        do {
            let event = try await client.sendGameEvent(
                QQTGameEventPayload(schema: .placeBomb, text: "place-bomb", roomId: currentRoom?.id)
            )
            lastBombLog = "[\(stamp)] 0x\(String(event.schema.rawValue, radix: 16)) \(event.text)"
        } catch {
            lastBombLog = "[\(stamp)] 放炸弹失败：\(error.localizedDescription)"
        }
    }

    private func mergeRooms(_ incoming: [GameRoom]) {
        var byID = Dictionary(uniqueKeysWithValues: rooms.map { ($0.id, $0) })
        for room in incoming {
            byID[room.id] = room
        }
        rooms = byID.values.sorted { $0.id < $1.id }
    }

    private func enter(_ room: GameRoom) {
        if let index = rooms.firstIndex(where: { $0.id == room.id }) {
            rooms[index] = room
        } else {
            rooms.insert(room, at: 0)
        }
        currentRoom = room
        chatLines = [ChatLine(text: "系统：欢迎来到 \(room.name) · 地图 \(room.mapName)")]
        screen = .room
    }

    private func appendChat(_ text: String) {
        chatLines.append(ChatLine(text: "系统：\(text)"))
        if chatLines.count > 40 {
            chatLines.removeFirst(chatLines.count - 40)
        }
    }

    private func sitLocal(_ room: inout GameRoom, uid: Int64, nickname: String, asHost: Bool) {
        guard let index = room.seats.firstIndex(where: { !$0.isOccupied }) else { return }
        room.seats[index].uid = uid
        room.seats[index].nickname = nickname
        room.seats[index].isReady = false
        room.seats[index].isHost = asHost
        room.seats[index].characterCode = uid == account?.uid ? selectedRoleCode : "boy"
    }

    private static func seedRooms() -> [GameRoom] {
        let catalog = MapCatalog.load().filter { $0.maxPlayers >= 2 }
        let fallback = MapCatalog.defaultEntry
        let maps = Array(catalog.prefix(8))
        let selectedMaps = maps.isEmpty ? Array(repeating: fallback, count: 8) : maps
        var result: [GameRoom] = []
        for index in 0..<8 {
            let map = selectedMaps[index % selectedMaps.count]
            var room = makeEmptyRoom(
                id: 101 + index,
                name: index == 0 ? "\(map.name) 新手房" : "\(map.name) \(index + 1)号房",
                channel: .casual,
                map: map
            )
            let botCount = (index * 3 + 1) % 6
            let botNames = ["糖糖", "海王子", "火影", "小悟空", "泰坦"]
            let botRoles = ["girl", "hwz", "hy", "xwk", "tt"]
            for botIndex in 0..<botCount {
                room.seats[botIndex].uid = Int64(900_000 + index * 10 + botIndex)
                room.seats[botIndex].nickname = botNames[botIndex % 5]
                room.seats[botIndex].characterCode = botRoles[botIndex % 5]
                room.seats[botIndex].isHost = botIndex == 0
                room.seats[botIndex].isReady = botIndex > 0
            }
            result.append(room)
        }

        var arena = makeEmptyRoom(
            id: 201,
            name: "高手竞技场",
            channel: .arena,
            map: selectedMaps.first ?? fallback
        )
        arena.seats[0].uid = 990_001
        arena.seats[0].nickname = "擂台房主"
        arena.seats[0].isHost = true
        result.append(arena)
        return result
    }

    private static func seedServerAreas() -> [ServerArea] {
        [
            ServerArea(id: 0, name: "上海电信", ping: 28),
            ServerArea(id: 1, name: "广东电信", ping: 36),
            ServerArea(id: 2, name: "北京网通", ping: 49),
            ServerArea(id: 3, name: "四川电信", ping: 58),
            ServerArea(id: 4, name: "湖北电信", ping: 66),
            ServerArea(id: 5, name: "浙江电信", ping: 42),
            ServerArea(id: 6, name: "江苏电信", ping: 45),
            ServerArea(id: 7, name: "山东网通", ping: 72),
            ServerArea(id: 8, name: "辽宁网通", ping: 84)
        ]
    }

    private static func seedSections() -> [GameSection] {
        let chineseNumbers = [
            "一", "二", "三", "四", "五", "六", "七",
            "八", "九", "十", "十一", "十二", "十三", "十四"
        ]
        return seedServerAreas().flatMap { area in
            SectionChannel.allCases.flatMap { channel in
                chineseNumbers.enumerated().map { index, number in
                    let population: SectionPopulation
                    switch (area.id * 3 + channel.rawValue * 5 + index) % 12 {
                    case 0: population = .full
                    case 1: population = .crowded
                    case 2, 3, 4: population = .busy
                    case 5, 6, 7, 8: population = .smooth
                    default: population = .open
                    }
                    return GameSection(
                        id: area.id * 1_000 + channel.rawValue * 100 + index + 1,
                        name: "\(channel.title.replacingOccurrences(of: "频道", with: ""))\(number)区",
                        channel: channel,
                        areaID: area.id,
                        population: population,
                        ping: area.ping + index * 3
                    )
                }
            }
        }
    }

    private static func makeEmptyRoom(id: Int, name: String, channel: ChannelKind, map: LegacyMapEntry) -> GameRoom {
        let seats = (1...8).map { index in
            Seat(index: index, uid: nil, nickname: nil, team: index <= 4 ? .red : .blue, isReady: false, isHost: false)
        }
        return GameRoom(
            id: id,
            name: name,
            mapId: map.id,
            mapFile: map.mapFile,
            mapName: map.name,
            channel: channel,
            status: .waiting,
            seats: seats
        )
    }

    private static func makeRoom(_ snapshot: QQTRoomSnapshot) -> GameRoom {
        GameRoom(
            id: snapshot.id,
            name: snapshot.name,
            mapId: snapshot.mapId,
            mapFile: snapshot.mapFile,
            mapName: snapshot.mapName,
            channel: ChannelKind(rawValue: snapshot.channel) ?? .casual,
            status: snapshot.status == "playing" ? .playing : .waiting,
            seats: snapshot.seats.map { seat in
                Seat(
                    index: seat.index,
                    uid: seat.uid,
                    nickname: seat.nickname,
                    team: TeamSide(rawValue: seat.team) ?? .red,
                    isReady: seat.isReady,
                    isHost: seat.isHost,
                    characterCode: seat.characterCode ?? "boy"
                )
            }
        )
    }
}
