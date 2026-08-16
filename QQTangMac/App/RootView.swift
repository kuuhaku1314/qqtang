import SwiftUI

struct RootView: View {
    @EnvironmentObject private var session: GameSession

    var body: some View {
        ZStack {
            if session.screen == .login {
                // 登录屏按原版 800×600 逻辑坐标自绘（含背景）
                LoginView()
            } else {
                LegacyImageView(image: session.loginBackgroundImage())
                    .scaledToFill()
                    .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
                    .clipped()
                    .opacity(0.55)

                Color.black.opacity(0.18)

                switch session.screen {
                case .login:
                    EmptyView()
                case .sectionSelect:
                    SectionSelectView()
                case .lobby:
                    LobbyView()
                case .room:
                    RoomView()
                case .battle:
                    BattleView()
                }
            }
        }
        .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
        .onAppear {
            MusicPlayer.shared.syncTo(screen: session.screen, battleMapType: battleMapType)
        }
        .onChange(of: session.screen) { _, screen in
            MusicPlayer.shared.syncTo(screen: screen, battleMapType: battleMapType)
        }
    }

    private var battleMapType: String? {
        guard let mapId = session.currentRoom?.mapId else { return nil }
        return MapCatalog.entry(id: mapId)?.type
    }
}
