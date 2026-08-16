import AppKit

/// 原版音效（QQTang4.3_Beta1Build2/sound，编号对照 QQTPVE 命名反查）：
/// soundMain=X01、soundLeave=X03、放泡=X09、爆炸=X34、被泡=X24、死亡=X12。
enum LegacySound: String {
    case main = "ui-main"
    case normal = "ui-normal"
    case leave = "ui-leave"
    case fail = "ui-fail"
    case readyGo = "ready-go"
    case bomb
    case explode
    case trap
    case heroDead = "hero-dead"
    case item
    case win
}

enum SoundPlayer {
    private static var cache: [String: NSSound] = [:]

    static func play(_ sound: LegacySound) {
        let name = sound.rawValue
        if let cached = cache[name] {
            if cached.isPlaying {
                (cached.copy() as? NSSound)?.play()
            } else {
                cached.play()
            }
            return
        }
        guard let url = Legacy43Assets.fileURL("Audio/\(name).wav"),
              let loaded = NSSound(contentsOf: url, byReference: true)
        else { return }
        cache[name] = loaded
        loaded.play()
    }
}
