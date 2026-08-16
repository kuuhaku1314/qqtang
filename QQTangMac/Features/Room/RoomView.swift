import SwiftUI

/// 4.3 房间界面；控件矩形与素材逐项对应 `uiRoom.pyc`（UI_children_room）。
/// 全部图片来自官方 QQTang4.3_Beta1Build2 安装包 `data/object.pkg`。
struct RoomView: View {
    @EnvironmentObject private var session: GameSession
    var snapshotMode = false

    @State private var chatDraft = ""
    @State private var dialog: RoomDialog?
    @State private var pendingMapID: Int?
    @State private var roleTab = 0
    @State private var rolePage = 0
    @State private var hoveredRole: String?

    private enum RoomDialog: Equatable {
        case mode
        case property
        case map
    }

    /// player1-8 rect：(10/132/254/376, 75/345) 114×102
    private let seatOrigins: [CGPoint] = [
        CGPoint(x: 10, y: 75),
        CGPoint(x: 132, y: 75),
        CGPoint(x: 254, y: 75),
        CGPoint(x: 376, y: 75),
        CGPoint(x: 10, y: 345),
        CGPoint(x: 132, y: 345),
        CGPoint(x: 254, y: 345),
        CGPoint(x: 376, y: 345),
    ]

    /// role1-8 rect：(548/601/654/707, 214/267) 50×50
    private let roleOrigins: [CGPoint] = [
        CGPoint(x: 548, y: 214),
        CGPoint(x: 601, y: 214),
        CGPoint(x: 654, y: 214),
        CGPoint(x: 707, y: 214),
        CGPoint(x: 548, y: 267),
        CGPoint(x: 601, y: 267),
        CGPoint(x: 654, y: 267),
        CGPoint(x: 707, y: 267),
    ]

    /// 每页 8 个角色（原版 defRoleCnt=8，左右箭头翻页）。
    private var rolePageCount: Int {
        let roster = roleTab == 0 ? RoleCatalog.normalRoster : RoleCatalog.vipRoster
        return max(1, (roster.count + 7) / 8)
    }

    private var visibleRoles: [String] {
        let roster = roleTab == 0 ? RoleCatalog.normalRoster : RoleCatalog.vipRoster
        let start = min(rolePage * 8, max(0, roster.count - 1))
        return Array(roster.dropFirst(start).prefix(8))
    }

    /// teamColor1-8 x 坐标（y=324, 30×35）。
    private let teamColorXs: [CGFloat] = [525, 557, 589, 622, 655, 687, 721, 753]

    var body: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Room/background.png")
                .frame(width: 800, height: 600)

            roomTitle
            seats
            mapPanel
            roomControlButtons
            rolePicker
            teamColorRow
            storageButton
            chatPanel
            primaryButton
            leaveButton

            if let dialog {
                dialogLayer(dialog)
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
        .clipped()
        .onAppear {
            pendingMapID = session.currentRoom?.mapId
        }
    }

    // MARK: - 顶栏 / 地图

    /// roomName rect (20,47,500,12)
    private var roomTitle: some View {
        Text(session.currentRoom?.name ?? "游戏房间")
            .font(.system(size: 12, weight: .bold))
            .foregroundStyle(.white)
            .shadow(color: Color(red: 0.26, green: 0.37, blue: 0.52), radius: 0, x: 1, y: 1)
            .lineLimit(1)
            .frame(width: 500, height: 12, alignment: .leading)
            .offset(x: 20, y: 47)
    }

    private var mapPanel: some View {
        Group {
            // mapPic rect (637,48,150,125)
            if let map = session.currentMap,
               let image = session.mapPreviewImage(for: map)
            {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.none)
                    .aspectRatio(contentMode: .fill)
                    .frame(width: 150, height: 125)
                    .clipped()
                    .offset(x: 637, y: 48)
            } else {
                Legacy43Image(path: "Room/random-map.png")
                    .aspectRatio(contentMode: .fill)
                    .frame(width: 150, height: 125)
                    .clipped()
                    .offset(x: 637, y: 48)
            }

            // mapIcon rect (650,165)，object/ui/map/midIcon/%s.img 原尺寸
            legacyNativeImage(midIconPath)
                .offset(x: 650, y: 165)

            // mapName rect (700,178,80,12)，黄字蓝边
            Text(session.currentMap?.name.replacingOccurrences(of: "\n", with: "") ?? "随机地图")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(Color(red: 1.0, green: 0.88, blue: 0.32))
                .shadow(color: Color(red: 0.0, green: 0.19, blue: 0.68), radius: 0, x: 1, y: 1)
                .lineLimit(1)
                .frame(width: 84, height: 12, alignment: .leading)
                .offset(x: 700, y: 178)
        }
    }

    private var midIconPath: String {
        let type = session.currentMap.flatMap { MapCatalog.entry(id: $0.id)?.type } ?? "rand"
        if Legacy43Assets.image("Room/midIcon-\(type).png") != nil {
            return "Room/midIcon-\(type).png"
        }
        return "Room/midIcon-rand.png"
    }

    // MARK: - 右侧按钮列（selModeBtn / roomModifyBtn / selMapBtn，95×35）

    private var roomControlButtons: some View {
        Group {
            originalStateButton(
                "Room/selMode", size: CGSize(width: 95, height: 35),
                enabled: session.isCurrentUserHost
            ) {
                dialog = dialog == .mode ? nil : .mode
            }
            .offset(x: 525, y: 59)

            originalStateButton(
                "Room/roomProp", size: CGSize(width: 95, height: 35),
                enabled: session.isCurrentUserHost
            ) {
                dialog = dialog == .property ? nil : .property
            }
            .offset(x: 525, y: 96)

            originalStateButton(
                "Room/selMap", size: CGSize(width: 95, height: 35),
                enabled: session.isCurrentUserHost
            ) {
                pendingMapID = session.currentRoom?.mapId
                dialog = dialog == .map ? nil : .map
            }
            .offset(x: 525, y: 133)
        }
    }

    // MARK: - 角色选择（normalRole/vipRole tabs + leftRole/rightRole + role1-8）

    private var rolePicker: some View {
        Group {
            // tab hotrect (517,192,45,19) / (562,192,45,19)
            Button {
                roleTab = 0
                rolePage = 0
            } label: {
                Legacy43Image(path: "Room/tab-normalChar-\(roleTab == 0 ? 1 : 0).png")
                    .frame(width: 45, height: 19)
            }
            .buttonStyle(.plain)
            .offset(x: 517, y: 192)

            Button {
                roleTab = 1
                rolePage = 0
            } label: {
                Legacy43Image(path: "Room/tab-vipChar-\(roleTab == 1 ? 1 : 0).png")
                    .frame(width: 45, height: 19)
            }
            .buttonStyle(.plain)
            .offset(x: 562, y: 192)

            // btn_leftRole (518,210,25,129) / btn_rightRole (769,210,25,129)：翻页
            Legacy43Button(
                normal: "Room/leftRole-0.png",
                hovered: "Room/leftRole-1.png",
                pressed: "Room/leftRole-2.png",
                size: CGSize(width: 25, height: 120)
            ) {
                rolePage = (rolePage - 1 + rolePageCount) % rolePageCount
            }
            .offset(x: 518, y: 214)

            Legacy43Button(
                normal: "Room/rightRole-0.png",
                hovered: "Room/rightRole-1.png",
                pressed: "Room/rightRole-2.png",
                size: CGSize(width: 25, height: 120)
            ) {
                rolePage = (rolePage + 1) % rolePageCount
            }
            .offset(x: 769, y: 214)

            // role1-8：图标 55×55（含内边距），画在槽位原点
            ForEach(Array(visibleRoles.enumerated()), id: \.element) { index, code in
                let origin = roleOrigins[index]
                roleSlot(code: code)
                    .offset(x: origin.x - 2, y: origin.y - 2)
            }

            // roleTip：悬停显示原版大立绘 + 名牌
            if let hoveredRole,
               let portrait = Legacy43Assets.image("Room/charBig-\(hoveredRole).png")
            {
                Image(nsImage: portrait)
                    .interpolation(.none)
                    .offset(x: 530, y: 45)
                    .allowsHitTesting(false)

                if let plate = Legacy43Assets.image("Room/charName-\(hoveredRole).png") {
                    Image(nsImage: plate)
                        .interpolation(.none)
                        .offset(x: 545, y: 282)
                        .allowsHitTesting(false)
                }
            }
        }
    }

    @ViewBuilder
    private func roleSlot(code: String) -> some View {
        let selected = code == session.selectedRoleCode
        Button {
            SoundPlayer.play(.normal)
            session.selectRole(code)
        } label: {
            legacyNativeImage("Room/charIcon-\(code)-\(selected ? 1 : 0).png")
                .frame(width: 55, height: 55, alignment: .topLeading)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering in
            if hovering {
                hoveredRole = code == "random" ? nil : code
            } else if hoveredRole == code {
                hoveredRole = nil
            }
        }
    }

    // MARK: - 队伍颜色（teamColor1-8，勾选 btn_check）

    private var teamColorRow: some View {
        Group {
            ForEach(0..<8, id: \.self) { index in
                let enabled = index < 2
                Button {
                    SoundPlayer.play(.normal)
                    session.setMyTeam(index == 0 ? .red : .blue)
                } label: {
                    Color.white.opacity(0.001)
                        .frame(width: 30, height: 35)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(!enabled)
                .offset(x: teamColorXs[index], y: 324)
            }

            if let team = session.currentUserSeat?.team {
                legacyNativeImage("Room/check.png")
                    .offset(x: teamColorXs[team == .red ? 0 : 1] + 4, y: 324 + 9)
            }
        }
    }

    /// storageBtn (525,374,67,64)
    private var storageButton: some View {
        Legacy43Button(
            normal: "Room/storage-0.png",
            hovered: "Room/storage-2.png",
            pressed: "Room/storage-3.png",
            size: CGSize(width: 67, height: 64)
        ) {}
            .offset(x: 525, y: 374)
    }

    // MARK: - 座位（btn_playerUp/Down 132×137 画布）

    private var seats: some View {
        ForEach(0..<8, id: \.self) { index in
            RoomSeatView(
                seatNumber: index + 1,
                player: player(at: index),
                isUpperSeat: index < 4,
                isCurrentUser: player(at: index)?.uid == session.account?.uid
            )
            .offset(x: seatOrigins[index].x, y: seatOrigins[index].y)
        }
    }

    // MARK: - 聊天（chatArea (7,478,500,115)）

    private var chatPanel: some View {
        Group {
            Legacy43Image(path: "Room/chat-small.png")
                .frame(width: 498, height: 89)
                .offset(x: 7, y: 478)

            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(Array(session.chatLines.suffix(5).enumerated()), id: \.offset) { index, line in
                            Text(line.text)
                                .font(.system(size: 11))
                                .foregroundStyle(.white)
                                .shadow(color: .black.opacity(0.6), radius: 1)
                                .lineLimit(1)
                                .id(index)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .scrollIndicators(.hidden)
                .frame(width: 462, height: 64)
                .offset(x: 22, y: 492)
                .onChange(of: session.chatLines.count) { _ in
                    let visibleCount = min(session.chatLines.count, 5)
                    if visibleCount > 0 {
                        proxy.scrollTo(visibleCount - 1, anchor: .bottom)
                    }
                }
            }

            // orientation (23+7,95+478,80,18)：聊天模式（综合）
            Text("综合")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(Color(red: 0.78, green: 0.95, blue: 0.99))
                .frame(width: 80, height: 18)
                .offset(x: 30, y: 573)

            // chatEdit (103+7,95+478,283,18)
            Group {
                if snapshotMode {
                    Text(chatDraft)
                        .font(.system(size: 12))
                        .foregroundStyle(.white)
                        .frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    TextField("", text: $chatDraft)
                        .textFieldStyle(.plain)
                        .font(.system(size: 12))
                        .foregroundStyle(.white)
                        .onSubmit(sendChat)
                }
            }
            .padding(.horizontal, 3)
            .frame(width: 283, height: 18)
            .offset(x: 110, y: 573)

            // sendBtn (417+7,94+478,34,22)
            Legacy43Button(
                normal: "Room/send-0.png",
                hovered: "Room/send-2.png",
                pressed: "Room/send-3.png",
                size: CGSize(width: 34, height: 22)
            ) {
                sendChat()
            }
            .offset(x: 424, y: 572)
        }
    }

    // MARK: - 开始 / 准备 / 返回

    /// startBtn (542,528,114,54)：房主=btn_start，其他人=btn_ready/btn_unready
    @ViewBuilder
    private var primaryButton: some View {
        if session.isCurrentUserHost {
            if session.canHostStart {
                Legacy43Button(
                    normal: "Room/start-0.png",
                    hovered: "Room/start-2.png",
                    pressed: "Room/start-3.png",
                    size: CGSize(width: 114, height: 54),
                    sound: .main
                ) {
                    session.startMatch()
                }
                .offset(x: 542, y: 528)
            } else {
                Legacy43Image(path: "Room/start-1.png")
                    .frame(width: 114, height: 54)
                    .offset(x: 542, y: 528)
            }
        } else {
            let stem = session.isCurrentUserReady ? "Room/unready" : "Room/ready"
            Legacy43Button(
                normal: "\(stem)-0.png",
                hovered: "\(stem)-2.png",
                pressed: "\(stem)-3.png",
                size: CGSize(width: 114, height: 54)
            ) {
                session.toggleReady()
            }
            .offset(x: 542, y: 528)
        }
    }

    /// leave (715,534,62,45)：object/ui/common/btn_return.img 52×48
    private var leaveButton: some View {
        Legacy43Button(
            normal: "Room/return-0.png",
            hovered: "Room/return-2.png",
            pressed: "Room/return-3.png",
            size: CGSize(width: 52, height: 48),
            sound: .leave
        ) {
            session.leaveRoom()
        }
        .offset(x: 715, y: 534)
    }

    // MARK: - 对话框

    @ViewBuilder
    private func dialogLayer(_ dialog: RoomDialog) -> some View {
        switch dialog {
        case .mode:
            modeDialog
        case .property:
            propertyDialog
        case .map:
            mapDialog
        }
    }

    private var modeDialog: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Room/select-mode-dialog.png")
                .frame(width: 171, height: 152)

            Text("● 竞技")
                .foregroundStyle(Color(red: 0.13, green: 0.49, blue: 0.74))
                .offset(x: 20, y: 31)
            Text("○ 探险")
                .foregroundStyle(Color(red: 0.13, green: 0.49, blue: 0.74))
                .offset(x: 92, y: 31)
            Text("● 无道具")
                .foregroundStyle(Color(red: 0.13, green: 0.49, blue: 0.74))
                .offset(x: 20, y: 68)
        }
        .font(.system(size: 11, weight: .bold))
        .frame(width: 171, height: 152)
        .offset(x: 620, y: 46)
        .onTapGesture { dialog = nil }
    }

    private var propertyDialog: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Room/room-property-dialog.png")
                .frame(width: 171, height: 152)

            Text("● 自由   ○ 标准")
                .offset(x: 20, y: 31)
            Text(session.currentRoom?.name ?? "")
                .lineLimit(1)
                .frame(width: 100, alignment: .leading)
                .offset(x: 50, y: 57)
            Text("○ 是")
                .offset(x: 50, y: 82)
            Text("未设置")
                .foregroundStyle(.secondary)
                .offset(x: 50, y: 105)
        }
        .font(.system(size: 11, weight: .bold))
        .foregroundStyle(Color(red: 0.13, green: 0.49, blue: 0.74))
        .frame(width: 171, height: 152)
        .offset(x: 620, y: 46)
        .onTapGesture { dialog = nil }
    }

    private var mapDialog: some View {
        ZStack(alignment: .topLeading) {
            Color.black.opacity(0.42)
                .frame(width: 800, height: 600)
                .onTapGesture { dialog = nil }

            Legacy43Image(path: "Room/select-map-dialog.png")
                .frame(width: 327, height: 549)
                .offset(x: 466, y: 48)

            ScrollView {
                LazyVStack(spacing: 3) {
                    ForEach(session.mapCatalog) { map in
                        Button {
                            pendingMapID = map.id
                        } label: {
                            HStack(spacing: 5) {
                                RoundedRectangle(cornerRadius: 2)
                                    .fill(
                                        map.id == pendingMapID
                                            ? Color.yellow
                                            : Color(red: 0.35, green: 0.80, blue: 0.96)
                                    )
                                    .frame(width: 8, height: 8)
                                Text(map.name)
                                    .lineLimit(1)
                                Spacer(minLength: 0)
                            }
                            .font(.system(size: 11, weight: map.id == pendingMapID ? .bold : .regular))
                            .foregroundStyle(map.id == pendingMapID ? Color.orange : Color.white)
                            .padding(.horizontal, 7)
                            .frame(width: 120, height: 27)
                            .background(
                                RoundedRectangle(cornerRadius: 4)
                                    .fill(
                                        map.id == pendingMapID
                                            ? Color(red: 0.12, green: 0.50, blue: 0.79)
                                            : Color(red: 0.12, green: 0.32, blue: 0.49).opacity(0.78)
                                    )
                            )
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.vertical, 4)
            }
            .scrollIndicators(.hidden)
            .frame(width: 120, height: 465)
            .offset(x: 482, y: 59)

            selectedMapPreview

            RoomTextButton(title: "确定", width: 104, height: 30) {
                if let pendingMapID {
                    session.selectCurrentRoomMap(pendingMapID)
                }
                dialog = nil
            }
            .offset(x: 644, y: 217)

            RoomTextButton(title: "取消", width: 104, height: 30) {
                pendingMapID = session.currentRoom?.mapId
                dialog = nil
            }
            .offset(x: 644, y: 254)
        }
    }

    private var selectedMapPreview: some View {
        Group {
            if let pendingMapID,
               let map = session.map(for: pendingMapID),
               let image = session.mapPreviewImage(for: map)
            {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.none)
                    .aspectRatio(contentMode: .fill)
                    .frame(width: 150, height: 112)
                    .clipped()
                    .offset(x: 637, y: 59)

                Text(map.name)
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(Color(red: 0.15, green: 0.48, blue: 0.70))
                    .frame(width: 150, height: 20)
                    .offset(x: 637, y: 174)
            }
        }
    }

    // MARK: - 工具

    private func originalStateButton(
        _ stem: String,
        size: CGSize,
        enabled: Bool = true,
        action: @escaping () -> Void
    ) -> some View {
        Group {
            if enabled {
                Legacy43Button(
                    normal: "\(stem)-0.png",
                    hovered: "\(stem)-1.png",
                    pressed: "\(stem)-2.png",
                    size: size,
                    action: action
                )
            } else {
                Legacy43Image(path: "\(stem)-3.png")
                    .frame(width: size.width, height: size.height)
            }
        }
    }

    /// 原尺寸位图（Legacy43Image 总是 resizable，这里保持像素尺寸）。
    private func legacyNativeImage(_ path: String) -> some View {
        Group {
            if let image = Legacy43Assets.image(path) {
                Image(nsImage: image)
                    .interpolation(.none)
            }
        }
    }

    private func player(at index: Int) -> Seat? {
        guard let seats = session.currentRoom?.seats,
              seats.indices.contains(index),
              seats[index].isOccupied
        else {
            return nil
        }
        return seats[index]
    }

    private func sendChat() {
        session.sendChat(chatDraft)
        chatDraft = ""
    }
}

/// 单个座位：门 btn_playerUp/Down（132×137 画布，帧 1 常态），
/// 子控件矩形来自 player1.children。
private struct RoomSeatView: View {
    let seatNumber: Int
    let player: Seat?
    let isUpperSeat: Bool
    let isCurrentUser: Bool

    var body: some View {
        ZStack(alignment: .topLeading) {
            if let image = Legacy43Assets.image(
                isUpperSeat ? "Room/playerUp-1.png" : "Room/playerDown-1.png"
            ) {
                Image(nsImage: image)
                    .interpolation(.none)
            }

            if let player {
                // teamColorStrip rect (8,5)，帧=队伍色
                if let strip = Legacy43Assets.image("Room/teamColorStrip-\(player.team.rawValue).png") {
                    Image(nsImage: strip)
                        .interpolation(.none)
                        .offset(x: 8, y: 5)
                }

                // name rect (50,9,96,12)
                Text(player.nickname ?? "玩家")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(isCurrentUser ? Color(red: 1.0, green: 0.93, blue: 0.45) : .white)
                    .shadow(color: .black.opacity(0.65), radius: 0, x: 1, y: 1)
                    .lineLimit(1)
                    .frame(width: 96, height: 12, alignment: .leading)
                    .offset(x: 50, y: 9)

                // 角色立绘：bg rect (22,28,96,96) 底部居中，随所选角色/队伍变化
                if let spritePath = RoleCatalog.standSprite(for: player.characterCode, team: player.team),
                   let sprite = Legacy43Assets.image(spritePath)
                {
                    Image(nsImage: sprite)
                        .interpolation(.none)
                        .offset(
                            x: 22 + (96 - sprite.size.width) / 2,
                            y: 28 + 96 - sprite.size.height - 6
                        )
                } else if let icon = Legacy43Assets.image("Room/charIcon-\(player.characterCode)-0.png") {
                    Image(nsImage: icon)
                        .interpolation(.none)
                        .offset(x: 22 + (96 - 55) / 2, y: 28 + (96 - 55) / 2)
                }

                // master rect (10,10,31,31)
                if player.isHost, let crown = Legacy43Assets.image("Room/master.png") {
                    Image(nsImage: crown)
                        .interpolation(.none)
                        .offset(x: 10, y: 10)
                }

                // ready rect (0,45)：img_ready.img 竖排「准备」章
                if player.isReady, !player.isHost,
                   let mark = Legacy43Assets.image("Room/ready-mark.png")
                {
                    Image(nsImage: mark)
                        .interpolation(.none)
                        .offset(x: 0, y: 45)
                }

                // no rect (20,20,16,18)：initlayer 99999，压在皇冠之上
                SeatNumberDigit(digit: seatNumber)
                    .offset(x: 20, y: 20)
            }
        }
        .frame(width: 132, height: 137, alignment: .topLeading)
    }
}

/// number4.img 数字条（16×18/字，顺序 0-9 / : - + .）。
private struct SeatNumberDigit: View {
    let digit: Int

    var body: some View {
        if let strip = Legacy43Assets.image("Room/num4.png") {
            ZStack(alignment: .topLeading) {
                Image(nsImage: strip)
                    .interpolation(.none)
                    .offset(x: -CGFloat(digit) * 16)
            }
            .frame(width: 16, height: 18, alignment: .topLeading)
            .clipped()
        }
    }
}

private struct RoomTextButton: View {
    let title: String
    let width: CGFloat
    let height: CGFloat
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
                .font(.system(size: height > 40 ? 18 : 12, weight: .bold))
                .foregroundStyle(.white)
                .shadow(color: Color(red: 0.03, green: 0.28, blue: 0.55), radius: 1, x: 1, y: 1)
                .frame(width: width, height: height)
                .background(
                    LinearGradient(
                        colors: [
                            Color(red: 0.40, green: 0.92, blue: 1.0),
                            Color(red: 0.08, green: 0.55, blue: 0.90),
                        ],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                .clipShape(RoundedRectangle(cornerRadius: height > 40 ? 13 : 7))
                .overlay(
                    RoundedRectangle(cornerRadius: height > 40 ? 13 : 7)
                        .stroke(.white.opacity(0.92), lineWidth: 2)
                )
        }
        .buttonStyle(.plain)
    }
}
