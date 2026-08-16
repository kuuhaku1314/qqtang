import SpriteKit
import SwiftUI

struct BattleView: View {
    @EnvironmentObject private var session: GameSession
    @State private var scene = BattleScene(size: CGSize(width: 800, height: 540))
    @State private var hud = BattleHUDState()

    var body: some View {
        let battleMap = session.battleMap(for: session.currentRoom)

        ZStack(alignment: .topLeading) {
            Color(red: 0.04, green: 0.14, blue: 0.18)
                .frame(width: 800, height: 600)

            SpriteView(scene: scene)
                .frame(width: 800, height: 540)
                .focusable()
                .onKeyPress(phases: [.down, .up]) { press in
                    let isDown = press.phase == .down
                    if let code = keyCode(for: press.key) {
                        scene.handleKey(code: code, isDown: isDown)
                        return .handled
                    }
                    return .ignored
                }

            playerListPanel(map: battleMap)
            statusBar
            leaveButton

            if hud.isFinished {
                resultDialog
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
        .clipped()
        .onAppear {
            scene.scaleMode = .aspectFill
            scene.playerSpriteCode = session.battleSpriteCode
            scene.participants = session.battleParticipants()
            scene.configure(map: battleMap)
            scene.onBombPlaceholder = {
                session.logBombPlaceholder()
            }
            scene.onStateChanged = { state in
                let justFinished = state.isFinished && !hud.isFinished
                hud = state
                if justFinished {
                    // 原版：战斗结束切胜负音乐
                    if state.resultText?.contains("胜利") == true {
                        MusicPlayer.shared.playOnce("PlayerWin")
                    } else {
                        MusicPlayer.shared.playOnce("PlayerLoss")
                    }
                }
            }
        }
        .onChange(of: session.currentRoom?.mapId) { _, _ in
            scene.configure(map: session.battleMap(for: session.currentRoom))
        }
        .onExitCommand(perform: session.exitBattle)
    }

    private func playerListPanel(map: BattleMap) -> some View {
        ZStack(alignment: .topLeading) {
            if Legacy43Assets.image("Game/player-list.png") != nil {
                Legacy43Image(path: "Game/player-list.png")
                    .frame(width: 191, height: 540)
            } else {
                LinearGradient(
                    colors: [
                        Color(red: 0.08, green: 0.36, blue: 0.54),
                        Color(red: 0.02, green: 0.18, blue: 0.30),
                    ],
                    startPoint: .top,
                    endPoint: .bottom
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12)
                        .stroke(Color.cyan.opacity(0.65), lineWidth: 2)
                )
            }

            Text(map.name)
                .font(.system(size: 12, weight: .heavy))
                .foregroundStyle(Color(red: 0.96, green: 1.0, blue: 0.66))
                .shadow(color: .black, radius: 1)
                .lineLimit(1)
                .frame(width: 82, height: 18)
                .offset(x: 102, y: 14)

            Text(clockText)
                .font(.system(size: 31, weight: .black, design: .rounded))
                .monospacedDigit()
                .foregroundStyle(.white)
                .shadow(color: Color(red: 0.02, green: 0.26, blue: 0.48), radius: 1, x: 2, y: 2)
                .frame(width: 165, height: 52)
                .offset(x: 17, y: 49)

            VStack(spacing: 4) {
                HStack(spacing: 8) {
                    Legacy43Image(path: "Game/Player/\(session.battleSpriteCode)-c1-down-1.png")
                        .frame(width: 38, height: 52)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(session.account?.nickname ?? "玩家")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundStyle(.white)
                        Text("Lv.\(session.account?.level ?? 1)")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundStyle(.yellow)
                        HStack(spacing: 2) {
                            ForEach(0..<3, id: \.self) { _ in
                                Circle()
                                    .fill(Color(red: 0.25, green: 0.92, blue: 1.0))
                                    .frame(width: 8, height: 8)
                            }
                        }
                    }
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 10)
                .frame(height: 64)

                Divider().overlay(Color.white.opacity(0.25))

                infoRow("得分", value: String(format: "%05d", hud.score))
                infoRow("目标", value: "敌方剩 \(hud.enemiesRemaining) 人")
                infoRow("糖泡", value: hud.bombsAvailable > 0 ? "剩 \(hud.bombsAvailable) 个" : "引爆中")
            }
            .frame(width: 174)
            .offset(x: 9, y: 94)

            VStack(alignment: .leading, spacing: 7) {
                Text("操作")
                    .font(.system(size: 12, weight: .heavy))
                    .foregroundStyle(.yellow)
                Text("方向键 / WASD　移动")
                Text("空格　　　　　 放糖泡")
                Text("清除 12 个糖箱过关")
            }
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(.white.opacity(0.92))
            .padding(9)
            .frame(width: 174, alignment: .leading)
            .background(Color.black.opacity(0.22))
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .offset(x: 9, y: 371)

            if let log = session.lastBombLog {
                Text(log)
                    .font(.system(size: 8, design: .monospaced))
                    .foregroundStyle(.white.opacity(0.55))
                    .lineLimit(2)
                    .frame(width: 172, height: 30, alignment: .topLeading)
                    .offset(x: 10, y: 470)
            }
        }
        .frame(width: 191, height: 540, alignment: .topLeading)
        .offset(x: 609, y: 0)
    }

    private var statusBar: some View {
        ZStack(alignment: .topLeading) {
            if Legacy43Assets.image("Game/status-bar.png") != nil {
                Legacy43Image(path: "Game/status-bar.png")
                    .frame(width: 623, height: 60)
            } else {
                LinearGradient(
                    colors: [
                        Color(red: 0.11, green: 0.49, blue: 0.63),
                        Color(red: 0.03, green: 0.25, blue: 0.39),
                    ],
                    startPoint: .top,
                    endPoint: .bottom
                )
            }

            statusItem(symbol: "circle.hexagongrid.fill", title: "糖泡", value: "×1")
                .offset(x: 8, y: 5)
            statusItem(symbol: "arrow.up.left.and.arrow.down.right", title: "威力", value: "2格")
                .offset(x: 61, y: 5)
            statusItem(symbol: "figure.run", title: "速度", value: "标准")
                .offset(x: 114, y: 5)

            ForEach(0..<7, id: \.self) { index in
                RoundedRectangle(cornerRadius: 7)
                    .fill(Color.white.opacity(0.11))
                    .overlay(
                        RoundedRectangle(cornerRadius: 7)
                            .stroke(Color.white.opacity(0.30), lineWidth: 1)
                    )
                    .frame(width: 46, height: 46)
                    .offset(x: 185 + CGFloat(index) * 54, y: 6)
            }

            Text("1")
                .font(.system(size: 12, weight: .black))
                .foregroundStyle(.white)
                .frame(width: 12, height: 12)
                .offset(x: 222, y: 41)
        }
        .frame(width: 623, height: 60, alignment: .topLeading)
        .offset(x: 0, y: 540)
    }

    private func statusItem(symbol: String, title: String, value: String) -> some View {
        VStack(spacing: 0) {
            Image(systemName: symbol)
                .font(.system(size: 17, weight: .bold))
                .foregroundStyle(.white)
            Text(title)
                .font(.system(size: 8, weight: .bold))
                .foregroundStyle(.white.opacity(0.82))
            Text(value)
                .font(.system(size: 8, weight: .black))
                .foregroundStyle(.yellow)
        }
        .frame(width: 48, height: 50)
    }

    private var leaveButton: some View {
        Group {
            if Legacy43Assets.image("Game/leave-3.png") != nil {
                Legacy43Button(
                    normal: "Game/leave-3.png",
                    hovered: "Game/leave-2.png",
                    pressed: "Game/leave-0.png",
                    size: CGSize(width: 52, height: 48),
                    action: session.exitBattle
                )
            } else {
                Button(action: session.exitBattle) {
                    Text("退出")
                        .font(.system(size: 13, weight: .heavy))
                        .foregroundStyle(.white)
                        .frame(width: 52, height: 48)
                        .background(Color(red: 0.10, green: 0.61, blue: 0.86))
                        .clipShape(RoundedRectangle(cornerRadius: 10))
                        .overlay(RoundedRectangle(cornerRadius: 10).stroke(.white, lineWidth: 2))
                }
                .buttonStyle(.plain)
            }
        }
        .offset(x: 734, y: 536)
    }

    private var resultDialog: some View {
        ZStack {
            Color.black.opacity(0.46)
                .frame(width: 800, height: 600)

            VStack(spacing: 11) {
                Text(hud.resultText == "功夫01 挑战完成" ? "挑战成功！" : "本局结束")
                    .font(.system(size: 27, weight: .black))
                    .foregroundStyle(hud.resultText == "功夫01 挑战完成" ? Color.yellow : Color.white)
                Text(hud.resultText ?? "")
                    .font(.system(size: 16, weight: .bold))
                    .foregroundStyle(.white)
                Text("得分 \(hud.score)")
                    .font(.system(size: 14, weight: .heavy, design: .monospaced))
                    .foregroundStyle(Color(red: 0.50, green: 0.95, blue: 1.0))

                HStack(spacing: 12) {
                    resultButton("再来一次") {
                        scene.restart()
                    }
                    resultButton("返回房间") {
                        session.exitBattle()
                    }
                }
                .padding(.top, 5)
            }
            .frame(width: 330, height: 190)
            .background(
                LinearGradient(
                    colors: [
                        Color(red: 0.16, green: 0.66, blue: 0.86),
                        Color(red: 0.04, green: 0.27, blue: 0.50),
                    ],
                    startPoint: .top,
                    endPoint: .bottom
                )
            )
            .clipShape(RoundedRectangle(cornerRadius: 20))
            .overlay(
                RoundedRectangle(cornerRadius: 20)
                    .stroke(Color.white.opacity(0.90), lineWidth: 3)
            )
            .shadow(color: .black.opacity(0.55), radius: 12)
        }
    }

    private func resultButton(_ title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title)
                .font(.system(size: 13, weight: .heavy))
                .foregroundStyle(Color(red: 0.04, green: 0.36, blue: 0.61))
                .frame(width: 105, height: 30)
                .background(Color.white)
                .clipShape(Capsule())
        }
        .buttonStyle(.plain)
    }

    private func infoRow(_ title: String, value: String) -> some View {
        HStack {
            Text(title)
                .foregroundStyle(.white.opacity(0.72))
            Spacer()
            Text(value)
                .foregroundStyle(.yellow)
        }
        .font(.system(size: 11, weight: .bold))
        .padding(.horizontal, 10)
        .frame(height: 22)
    }

    private var clockText: String {
        String(format: "%02d:%02d", hud.remainingSeconds / 60, hud.remainingSeconds % 60)
    }

    private func keyCode(for key: KeyEquivalent) -> UInt16? {
        switch key {
        case .upArrow: return 126
        case .downArrow: return 125
        case .leftArrow: return 123
        case .rightArrow: return 124
        case .space: return 49
        case "w", "W": return 13
        case "s", "S": return 1
        case "a", "A": return 0
        case "d", "D": return 2
        default: return nil
        }
    }
}
