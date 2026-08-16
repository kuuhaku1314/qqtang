import SwiftUI

struct LobbyView: View {
    @EnvironmentObject private var session: GameSession
    var snapshotMode = false
    @State private var mainMode: LobbyMode = .compete
    @State private var competeMode = 0
    @State private var playerTab = 0
    @State private var roomPage = 0
    @State private var chatInput = ""
    @State private var notice: String?

    private static let canvas = CGSize(width: 800, height: 600)

    private enum LobbyMode: Int, CaseIterable {
        case compete
        case explore
        case chat

        var assetName: String {
            switch self {
            case .compete: "compete"
            case .explore: "explore"
            case .chat: "chat"
            }
        }
    }

    var body: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Lobby/background.png")
                .frame(width: 800, height: 600)

            topIdentity
            modeTabs
            playerList
            chatPanel
            actionButtons
            roomGrid

            if let notice {
                noticeDialog(notice)
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
        .scaleEffect(Theme.windowSize.width / Self.canvas.width)
        .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
        .clipped()
        .onExitCommand { session.leaveLobby() }
    }

    private var topIdentity: some View {
        ZStack(alignment: .topLeading) {
            Text(session.selectedSection?.name ?? "小区大厅")
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(.white)
                .shadow(color: .black, radius: 1, x: 1, y: 1)
                .frame(width: 430, height: 18)
                .offset(x: 338, y: 44)

            Text(
                "\(session.selectedArea?.name ?? "")  "
                    + "\(session.selectedSectionChannel.title)"
            )
            .font(.system(size: 12, weight: .bold))
            .foregroundStyle(Color(red: 0.25, green: 0.37, blue: 0.52))
            .frame(width: 200, height: 16, alignment: .leading)
            .offset(x: 36, y: 559)
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private var modeTabs: some View {
        ZStack(alignment: .topLeading) {
            ForEach(Array(LobbyMode.allCases.enumerated()), id: \.element.rawValue) { index, mode in
                Button {
                    mainMode = mode
                } label: {
                    Legacy43Image(path: "Lobby/tab_\(mode.assetName)-\(mainMode == mode ? 1 : 0).png")
                        .frame(width: 98, height: 30)
                }
                .buttonStyle(.plain)
                .offset(x: [20.0, 122.0, 224.0][index], y: 39)
            }

            if mainMode == .compete {
                ForEach(0..<3, id: \.self) { index in
                    let names = ["noItem", "item", "honor"]
                    Button {
                        competeMode = index
                    } label: {
                        Legacy43Image(
                            path: "Lobby/tab_\(names[index])-\(competeMode == index ? 1 : 0).png"
                        )
                        .frame(width: 57, height: 25)
                    }
                    .buttonStyle(.plain)
                    .offset(x: [30.0, 94.0, 158.0][index], y: 70)
                }
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private var roomGrid: some View {
        let rooms = pageRooms
        return ZStack {
            ForEach(Array(rooms.enumerated()), id: \.element.id) { index, room in
                roomCard(room)
                    .frame(width: 181, height: 111)
                    .position(
                        x: roomX(index) + 90.5,
                        y: roomY(index) - 96 + 55.5
                    )
            }
        }
        .frame(width: 420, height: 464)
        .offset(x: 0, y: 96)
    }

    private func roomCard(_ room: GameRoom) -> some View {
        let canJoin = room.status == .waiting && room.occupiedCount < room.capacity
        return Button {
            SoundPlayer.play(.main)
            session.joinRoom(id: room.id)
        } label: {
            ZStack(alignment: .topLeading) {
                Color.white.opacity(0.001)
                    .frame(width: 181, height: 111)

                Text(String(format: "%03d", room.id))
                    .font(.system(size: 10, weight: .heavy, design: .monospaced))
                    .foregroundStyle(Color(red: 0.96, green: 0.61, blue: 0.16))
                    .frame(width: 30, height: 12, alignment: .center)
                    .offset(x: 15, y: 13)

                Text(room.name)
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(Color(red: 0.16, green: 0.35, blue: 0.57))
                    .lineLimit(1)
                    .frame(width: 93, height: 12, alignment: .leading)
                    .offset(x: 58, y: 15)

                if let icon = mapModeIcon(room) {
                    Image(nsImage: icon)
                        .interpolation(.none)
                        .offset(x: RoomCardChrome.mapIcon.x, y: RoomCardChrome.mapIcon.y)
                }

                Text(room.mapName.replacingOccurrences(of: "\n", with: ""))
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(Color(red: 1.0, green: 0.88, blue: 0.32))
                    .shadow(color: Color(red: 0.0, green: 0.19, blue: 0.68), radius: 0, x: 1, y: 1)
                    .lineLimit(1)
                    .frame(width: RoomCardChrome.mapName.width, height: RoomCardChrome.mapName.height, alignment: .leading)
                    .offset(x: RoomCardChrome.mapName.minX, y: RoomCardChrome.mapName.minY)

                Text("\(room.occupiedCount)/\(room.capacity)")
                    .font(.system(size: 11, weight: .black, design: .monospaced))
                    .foregroundStyle(room.occupiedCount >= room.capacity ? .red : .white)
                    .shadow(color: Color(red: 0.05, green: 0.23, blue: 0.4), radius: 1)
                    .frame(width: RoomCardChrome.playerCount.width, height: RoomCardChrome.playerCount.height, alignment: .leading)
                    .offset(x: RoomCardChrome.playerCount.minX, y: RoomCardChrome.playerCount.minY)

                if let badge = modeBadge(for: room) {
                    Image(nsImage: badge)
                        .interpolation(.none)
                        .offset(x: RoomCardChrome.badge.x, y: RoomCardChrome.badge.y)
                }
            }
            .frame(width: 181, height: 111, alignment: .topLeading)
            .contentShape(RoundedRectangle(cornerRadius: 17))
        }
        .buttonStyle(LobbyRoomButtonStyle())
        .disabled(!canJoin)
    }

    private var playerList: some View {
        ZStack(alignment: .topLeading) {
            ForEach(0..<3, id: \.self) { index in
                let names = ["player", "friend", "allKinMember"]
                Button {
                    playerTab = index
                } label: {
                    Legacy43Image(
                        path: "Lobby/tab_\(names[index])-\(playerTab == index ? 1 : 0).png"
                    )
                    .frame(width: 76, height: 19)
                }
                .buttonStyle(.plain)
                .offset(x: [432.0, 509.0, 587.0][index], y: index == 2 ? 66 : 67)
            }

            ForEach(Array(visiblePlayers.prefix(10).enumerated()), id: \.offset) { index, name in
                HStack(spacing: 8) {
                    Text(String(format: "%02d", index + 1))
                        .font(.system(size: 10, design: .monospaced))
                        .foregroundStyle(.white.opacity(0.5))
                    Text(name)
                        .font(.system(size: 12, weight: .bold))
                        .foregroundStyle(.white)
                    Spacer()
                    Text(index == 0 ? "Lv.\(session.account?.level ?? 1)" : "在线")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(index == 0 ? .yellow : .green)
                }
                .frame(width: 294, height: 20)
                .offset(x: 466, y: 103 + CGFloat(index) * 22)
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private var chatPanel: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Lobby/chat-normal.png")
                .frame(width: 344, height: 213)
                .offset(x: 442, y: 258)

            ScrollView {
                VStack(alignment: .leading, spacing: 3) {
                    ForEach(session.chatLines.suffix(8)) { line in
                        Text(line.text)
                            .font(.system(size: 11))
                            .foregroundStyle(.white)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .padding(.horizontal, 6)
                .padding(.vertical, 5)
            }
            .frame(width: 310, height: 172)
            .offset(x: 452, y: 277)

            Group {
                if snapshotMode {
                    Text(chatInput)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(.white)
                        .frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    TextField("", text: $chatInput)
                        .textFieldStyle(.plain)
                        .font(.system(size: 12, weight: .medium))
                        .foregroundStyle(.white)
                        .onSubmit { sendChat() }
                }
            }
            .padding(.horizontal, 5)
            .frame(width: 230, height: 19)
            .background(Color.black.opacity(0.14))
            .offset(x: 546, y: 499)

            Button(action: sendChat) {
                Text("发送")
                    .font(.system(size: 12, weight: .heavy))
                    .foregroundStyle(.white)
                    .frame(width: 57, height: 34)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .offset(x: 442, y: 533)
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private var actionButtons: some View {
        ZStack(alignment: .topLeading) {
            originalButton("shop", size: CGSize(width: 65, height: 58)) {
                notice = "4.3 商城入口已保留；当前纵切阶段不开放购买。"
            }
            .offset(x: 435, y: 536)

            originalButton("practice", size: CGSize(width: 51, height: 54)) {
                session.createRoomMapId = MapCatalog.defaultEntry.id
                session.createRoomName = "功夫01 新手练习"
                session.createRoom()
            }
            .offset(x: 505, y: 538)

            originalButton("quickJoin", size: CGSize(width: 51, height: 54)) {
                session.quickJoinRoom()
            }
            .offset(x: 560, y: 538)

            originalButton("createRoom", size: CGSize(width: 51, height: 54)) {
                session.createRoom()
            }
            .offset(x: 615, y: 538)

            originalButton("match", size: CGSize(width: 51, height: 54)) {
                session.selectedChannel = .arena
                notice = "已切换到竞技房间列表。"
            }
            .offset(x: 670, y: 538)

            Legacy43Button(
                normal: commonStatePath("leave", 3),
                hovered: commonStatePath("leave", 2),
                pressed: commonStatePath("leave", 0),
                size: CGSize(width: 52, height: 48),
                sound: .leave
            ) {
                session.leaveLobby()
            }
            .offset(x: 735, y: 542)

            originalButton("left", normalFrame: 3, hoveredFrame: 2, size: CGSize(width: 33, height: 35)) {
                roomPage = max(0, roomPage - 1)
            }
            .offset(x: 332, y: 550)

            originalButton("right", normalFrame: 3, hoveredFrame: 2, size: CGSize(width: 33, height: 35)) {
                roomPage += 1
            }
            .offset(x: 367, y: 550)
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private func originalButton(
        _ name: String,
        normalFrame: Int = 2,
        hoveredFrame: Int = 1,
        size: CGSize,
        action: @escaping () -> Void
    ) -> some View {
        Legacy43Button(
            normal: lobbyStatePath(name, normalFrame),
            hovered: lobbyStatePath(name, hoveredFrame),
            pressed: lobbyStatePath(name, 0),
            size: size,
            action: action
        )
    }

    private func noticeDialog(_ text: String) -> some View {
        ZStack(alignment: .topLeading) {
            Color.black.opacity(0.35).frame(width: 800, height: 600)
            Legacy43Image(path: "Common/message-box.png")
                .frame(width: 368, height: 326)
                .offset(x: 216, y: 137)
            Text(text)
                .font(.system(size: 15, weight: .bold))
                .foregroundStyle(.white)
                .multilineTextAlignment(.center)
                .frame(width: 310, height: 150)
                .offset(x: 245, y: 205)
            Button {
                notice = nil
            } label: {
                Text("确定")
                    .font(.system(size: 14, weight: .heavy))
                    .foregroundStyle(.white)
                    .frame(width: 80, height: 31)
            }
            .buttonStyle(.plain)
            .offset(x: 360, y: 413)
        }
    }

    private var pageRooms: [GameRoom] {
        guard mainMode == .compete else { return [] }
        let rooms = session.filteredRooms()
        let start = roomPage * 8
        guard start < rooms.count else {
            return roomPage == 0 ? rooms : []
        }
        return Array(rooms.dropFirst(start).prefix(8))
    }

    private var visiblePlayers: [String] {
        var names = [session.account?.nickname ?? "本地玩家"]
        if playerTab == 1 {
            names.append(contentsOf: ["糖糖", "海王子"])
        } else if playerTab == 2 {
            names.append("暂未加入家族")
        } else {
            names.append(
                contentsOf: session.filteredRooms()
                    .flatMap(\.seats)
                    .compactMap(\.nickname)
            )
        }
        return Array(names.uniqued())
    }

    private func sendChat() {
        session.sendChat(chatInput)
        chatInput = ""
    }

    private func mapModeIcon(_ room: GameRoom) -> NSImage? {
        let type = MapCatalog.entry(id: room.mapId)?.type ?? "rand"
        return LegacyAssetStore.shared.loadMapTypeIcon(type, large: true)
    }

    private func modeBadge(for room: GameRoom) -> NSImage? {
        let path = room.channel == .arena
            ? "res/uiRes/selRoom/icon/tubiao_biaozhun.img"
            : "res/uiRes/selRoom/icon/tubiao_ziyou.img"
        return LegacyAssetStore.shared.nsImage(relativePath: path)
    }

    private func lobbyStatePath(_ name: String, _ frame: Int) -> String {
        let path = "Lobby/\(name)-\(frame).png"
        if Legacy43Assets.image(path) != nil { return path }
        for fallback in [2, 3, 1, 0] {
            let alternate = "Lobby/\(name)-\(fallback).png"
            if Legacy43Assets.image(alternate) != nil { return alternate }
        }
        return path
    }

    private func commonStatePath(_ name: String, _ frame: Int) -> String {
        let path = "Common/\(name)-\(frame).png"
        if Legacy43Assets.image(path) != nil { return path }
        for fallback in [3, 2, 1, 0] {
            let alternate = "Common/\(name)-\(fallback).png"
            if Legacy43Assets.image(alternate) != nil { return alternate }
        }
        return path
    }

    private func roomX(_ index: Int) -> CGFloat {
        index.isMultiple(of: 2) ? 27 : 223
    }

    private func roomY(_ index: Int) -> CGFloat {
        101 + CGFloat(index / 2) * 112
    }
}

/// Baked `Lobby/background.png` room chrome: the recessed map panel
/// starts at (59, 38) size 111×62, not the original dlg_room (8, 40, 78, 20).
private enum RoomCardChrome {
    static let mapIcon = CGPoint(x: 66, y: 44)
    static let mapName = CGRect(x: 116, y: 50, width: 48, height: 12)
    static let playerCount = CGRect(x: 116, y: 76, width: 48, height: 19)
    static let badge = CGPoint(x: 148, y: 42)
}

private struct LobbyRoomButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .overlay {
                RoundedRectangle(cornerRadius: 17)
                    .stroke(
                        configuration.isPressed ? Color.yellow : Color.clear,
                        lineWidth: 2
                    )
            }
            .brightness(configuration.isPressed ? -0.12 : 0)
    }
}

private extension Sequence where Element: Hashable {
    func uniqued() -> [Element] {
        var seen = Set<Element>()
        return filter { seen.insert($0).inserted }
    }
}
