import Foundation

@MainActor
final class LocalRuntime: ObservableObject {
    static let shared = LocalRuntime()

    @Published private(set) var status = "协议未启动"
    @Published private(set) var directoryPort = 18080
    @Published private(set) var gamePort = 18000

    private let directory = DirectoryServer()
    private let game = GameServer()
    private var started = false

    func startIfNeeded() {
        guard !started else { return }
        started = true
        if let config = ProtocolConfig.load() {
            directoryPort = config.httpPort ?? 18080
            gamePort = config.directoryPort ?? 18000
        }
        do {
            try directory.start(address: "127.0.0.1:\(directoryPort)")
            try game.start(port: UInt16(gamePort))
            status = "协议就绪 127.0.0.1:\(directoryPort)/\(gamePort)（对齐 Windows qqt-server-local）"
        } catch {
            status = "协议启动失败：\(error.localizedDescription)"
        }
    }
}
