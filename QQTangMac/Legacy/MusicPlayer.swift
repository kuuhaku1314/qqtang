import AVFoundation

/// 原版背景音乐（uiConst.pyc）：
/// musicStart = m09（登录）、musicDirAndSection = das（选区/大厅）、
/// musicRoom = m10（房间）；战斗按地图城市取同名曲，胜负播 PlayerWin/PlayerLoss。
@MainActor
final class MusicPlayer {
    static let shared = MusicPlayer()

    private var player: AVAudioPlayer?
    private var currentName: String?

    func playLoop(_ name: String) {
        play(name, loops: -1)
    }

    func playOnce(_ name: String) {
        play(name, loops: 0)
    }

    func stop() {
        player?.stop()
        player = nil
        currentName = nil
    }

    private func play(_ name: String, loops: Int) {
        guard currentName != name else { return }
        guard let url = Legacy43Assets.fileURL("Audio/BGM/\(name).m4a"),
              let loaded = try? AVAudioPlayer(contentsOf: url)
        else { return }
        player?.stop()
        currentName = name
        loaded.numberOfLoops = loops
        loaded.volume = 0.5
        loaded.play()
        player = loaded
    }

    /// 屏幕切换时的原版曲目。
    func syncTo(screen: AppScreen, battleMapType: String?) {
        switch screen {
        case .login:
            playLoop("m09")
        case .sectionSelect, .lobby:
            playLoop("das")
        case .room:
            playLoop("m10")
        case .battle:
            let type = battleMapType ?? "M07"
            if Legacy43Assets.fileURL("Audio/BGM/\(type).m4a") != nil {
                playLoop(type)
            } else {
                playLoop("M07")
            }
        }
    }
}
