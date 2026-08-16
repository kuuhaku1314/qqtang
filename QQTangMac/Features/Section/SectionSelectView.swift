import AppKit
import SwiftUI

/// 4.3 选区页；控件坐标逐项对应 `uiSelSect.pyc`。
struct SectionSelectView: View {
    @EnvironmentObject private var session: GameSession
    @State private var showAreaDialog = false

    private static let canvas = CGSize(width: 800, height: 600)

    var body: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Section/background.png")
                .frame(width: 800, height: 600)

            noticePanel
            channelTabs
            sectionGrid
            actionButtons

            if showAreaDialog {
                areaDialog
            }

            if let error = session.loginError {
                errorDialog(error)
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
        .scaleEffect(Theme.windowSize.width / Self.canvas.width)
        .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
        .clipped()
        .onExitCommand { session.returnToLogin() }
    }

    private var noticePanel: some View {
        VStack(alignment: .leading, spacing: 11) {
            Text("适度游戏，有益健康")
                .font(.system(size: 15, weight: .bold))
                .foregroundStyle(Color(red: 0.92, green: 0.76, blue: 0.64))
            Text("欢迎回到 QQ堂 4.3《欢聚一堂》")
                .font(.system(size: 14, weight: .bold))
                .foregroundStyle(Color(red: 1, green: 0.86, blue: 0.36))
            Text("请选择右侧频道与小区。小区状态和网络延迟会实时显示。")
                .font(.system(size: 12))
                .foregroundStyle(.white.opacity(0.9))
                .fixedSize(horizontal: false, vertical: true)
            Spacer()
            Text("当前大区：\(session.selectedArea?.name ?? "未选择")")
                .font(.system(size: 13, weight: .bold))
                .foregroundStyle(.white)
        }
        .shadow(color: .black.opacity(0.8), radius: 1, x: 1, y: 1)
        .frame(width: 284, height: 304, alignment: .topLeading)
        .offset(x: 28, y: 255)
    }

    private var channelTabs: some View {
        ZStack(alignment: .topLeading) {
            ForEach(Array(SectionChannel.allCases.enumerated()), id: \.element.id) { index, channel in
                Button {
                    session.selectSectionChannel(channel)
                } label: {
                    Legacy43Image(path: tabPath(channel))
                        .frame(width: 72, height: 37)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .offset(x: tabX(index), y: index == 0 || index == 4 ? 59 : 58)
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private var sectionGrid: some View {
        let sections = Array(session.visibleSections.prefix(28))
        return ZStack(alignment: .topLeading) {
            ForEach(Array(sections.enumerated()), id: \.element.id) { index, section in
                sectionButton(section)
                    .offset(
                        x: index.isMultiple(of: 2) ? 383 : 580,
                        y: 97 + CGFloat(index / 2) * 29
                    )
            }
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private func sectionButton(_ section: GameSection) -> some View {
        Button {
            session.enterSection(section)
        } label: {
            ZStack(alignment: .topLeading) {
                Legacy43Image(path: "Section/section-row.png")
                    .frame(width: 187, height: 26)

                Text(section.name)
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(rowTextColor(section.population))
                    .shadow(color: .white.opacity(0.75), radius: 0, x: 1, y: 1)
                    .frame(width: 112, height: 20, alignment: .leading)
                    .offset(x: 20, y: 3)

                if Legacy43Assets.image("Section/state-\(section.population.rawValue).png") != nil {
                    Legacy43Image(path: "Section/state-\(section.population.rawValue).png")
                        .frame(width: 63, height: 14)
                        .offset(x: 118, y: 6)
                } else {
                    Text(section.population.title)
                        .font(.system(size: 10, weight: .heavy))
                        .foregroundStyle(.white)
                        .frame(width: 52, height: 14)
                        .background(populationColor(section.population))
                        .clipShape(Capsule())
                        .offset(x: 126, y: 6)
                }
            }
            .frame(width: 187, height: 26)
            .contentShape(Rectangle())
        }
        .buttonStyle(SectionRowButtonStyle())
        .disabled(section.population == .maintenance)
        .help("\(section.population.title) · \(section.ping) ms")
    }

    private var actionButtons: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Button(
                normal: statePath("Section/practice", normal: 2),
                hovered: statePath("Section/practice", normal: 1),
                pressed: statePath("Section/practice", normal: 0),
                size: CGSize(width: 51, height: 74)
            ) {
                session.selectSectionChannel(.practice)
                session.quickJoinSection()
            }
            .offset(x: 379, y: 513)

            Legacy43Button(
                normal: statePath("Section/selZone", normal: 2),
                hovered: statePath("Section/selZone", normal: 1),
                pressed: statePath("Section/selZone", normal: 0),
                size: CGSize(width: 51, height: 74)
            ) {
                showAreaDialog = true
            }
            .offset(x: 445, y: 513)

            Legacy43Button(
                normal: statePath("Section/quickJoin", normal: 2),
                hovered: statePath("Section/quickJoin", normal: 1),
                pressed: statePath("Section/quickJoin", normal: 0),
                size: CGSize(width: 51, height: 74)
            ) {
                session.quickJoinSection()
            }
            .offset(x: 511, y: 513)

            Legacy43Button(
                normal: statePath("Section/sysSetup", normal: 2),
                hovered: statePath("Section/sysSetup", normal: 1),
                pressed: statePath("Section/sysSetup", normal: 0),
                size: CGSize(width: 51, height: 74)
            ) {
                session.loginError = "系统设置将在后续阶段接入；当前可使用 macOS 音量与键盘设置。"
            }
            .offset(x: 577, y: 513)

            Legacy43Button(
                normal: statePath("Section/quit", normal: 3),
                hovered: statePath("Section/quit", normal: 2),
                pressed: statePath("Section/quit", normal: 0),
                size: CGSize(width: 52, height: 48)
            ) {
                session.returnToLogin()
            }
            .offset(x: 717, y: 520)
        }
        .frame(width: 800, height: 600, alignment: .topLeading)
    }

    private var areaDialog: some View {
        ZStack(alignment: .topLeading) {
            Color.black.opacity(0.32)
                .frame(width: 800, height: 600)
                .onTapGesture { showAreaDialog = false }

            Legacy43Image(path: "Section/server-dialog.png")
                .frame(width: 229, height: 331)
                .offset(x: 500, y: 200)

            ForEach(Array(session.serverAreas.prefix(9).enumerated()), id: \.element.id) { index, area in
                Button {
                    session.selectServerArea(area)
                } label: {
                    HStack(spacing: 4) {
                        Text(area.name)
                            .frame(width: 98, alignment: .center)
                        Text(networkBars(area.ping))
                            .font(.system(size: 10, design: .monospaced))
                    }
                    .font(.system(size: 12, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: 142, height: 22)
                    .background(
                        session.selectedAreaID == area.id
                            ? Color(red: 0.94, green: 0.45, blue: 0.18).opacity(0.75)
                            : Color.clear
                    )
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .offset(x: 528, y: 257 + CGFloat(index) * 23)
            }

            Legacy43Button(
                normal: "Common/confirm-normal.png",
                hovered: "Common/confirm-normal.png",
                pressed: "Common/confirm-normal.png",
                size: CGSize(width: 43, height: 31)
            ) {
                showAreaDialog = false
            }
            .offset(x: 590, y: 485)

            Button {
                showAreaDialog = false
            } label: {
                Legacy43Image(path: "Section/server-close.png")
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.plain)
            .offset(x: 700, y: 204)
        }
    }

    private func errorDialog(_ text: String) -> some View {
        ZStack(alignment: .topLeading) {
            Color.black.opacity(0.42).frame(width: 800, height: 600)
            Legacy43Image(path: "Common/message-box.png")
                .frame(width: 368, height: 326)
                .offset(x: 216, y: 137)
            Text(text)
                .font(.system(size: 15, weight: .bold))
                .foregroundStyle(.white)
                .multilineTextAlignment(.center)
                .frame(width: 310, height: 150)
                .offset(x: 245, y: 205)
            Legacy43Button(
                normal: "Common/close-normal.png",
                hovered: "Common/close-normal.png",
                pressed: "Common/close-normal.png",
                size: CGSize(width: 44, height: 31)
            ) {
                session.loginError = nil
            }
            .offset(x: 378, y: 419)
        }
    }

    private func tabPath(_ channel: SectionChannel) -> String {
        let selectedFrame = session.selectedSectionChannel == channel ? 1 : 0
        let path = "Section/tab-\(channel.assetName)-\(selectedFrame).png"
        if Legacy43Assets.image(path) != nil {
            return path
        }
        return "Section/tab-\(channel.assetName)-0.png"
    }

    private func statePath(_ prefix: String, normal frame: Int) -> String {
        let path = "\(prefix)-\(frame).png"
        if Legacy43Assets.image(path) != nil {
            return path
        }
        for fallback in [2, 3, 1, 0] {
            let fallbackPath = "\(prefix)-\(fallback).png"
            if Legacy43Assets.image(fallbackPath) != nil {
                return fallbackPath
            }
        }
        return path
    }

    private func tabX(_ index: Int) -> CGFloat {
        [385, 454, 524, 593, 661][index]
    }

    private func rowTextColor(_ population: SectionPopulation) -> Color {
        population == .full || population == .maintenance
            ? Color.gray.opacity(0.8)
            : Color(red: 0.19, green: 0.25, blue: 0.55)
    }

    private func populationColor(_ population: SectionPopulation) -> Color {
        switch population {
        case .open: .green
        case .smooth: .blue
        case .busy: .orange
        case .crowded: .red
        case .full: .purple
        case .maintenance: .gray
        }
    }

    private func networkBars(_ ping: Int) -> String {
        switch ping {
        case ..<50: "▂▄▆█"
        case ..<90: "▂▄▆·"
        default: "▂▄··"
        }
    }
}

private struct SectionRowButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .brightness(configuration.isPressed ? -0.12 : 0)
            .scaleEffect(configuration.isPressed ? 0.99 : 1)
    }
}
