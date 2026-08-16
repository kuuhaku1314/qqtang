import SpriteKit
import SwiftUI

@main
struct QQTangMacApp: App {
    @StateObject private var session = GameSession()
    @StateObject private var runtime = LocalRuntime.shared

    init() {
        Self.handleSnapshotModeIfNeeded()
    }

    /// 调试用：`QQTangMac --snapshot /tmp/out.png --snapshot-screen room`
    /// 可离屏渲染 login / section / lobby / room / battle。
    @MainActor
    private static func handleSnapshotModeIfNeeded() {
        let args = CommandLine.arguments
        guard let index = args.firstIndex(of: "--snapshot"), args.count > index + 1 else { return }
        let path = args[index + 1]
        let screenName: String
        if let screenIndex = args.firstIndex(of: "--snapshot-screen"),
           args.count > screenIndex + 1
        {
            screenName = args[screenIndex + 1]
        } else {
            screenName = "login"
        }

        let session = GameSession()
        if screenName == "battle-scene" {
            session.prepareSnapshot(screen: .battle)
            let map = session.battleMap(for: session.currentRoom)
            if let image = renderBattleScene(map: map, participants: session.battleParticipants()) {
                writePNG(image, to: path)
            }
            exit(0)
        }

        let content: AnyView
        switch screenName {
        case "section":
            session.prepareSnapshot(screen: .sectionSelect)
            content = AnyView(SectionSelectView())
        case "lobby":
            session.prepareSnapshot(screen: .lobby)
            content = AnyView(LobbyView(snapshotMode: true))
        case "room":
            session.prepareSnapshot(screen: .room)
            content = AnyView(RoomView(snapshotMode: true))
        case "battle":
            session.prepareSnapshot(screen: .battle)
            content = AnyView(BattleView())
        default:
            session.prepareSnapshot(screen: .login)
            content = AnyView(LoginView(snapshotMode: true))
        }

        let view = content
            .environmentObject(session)
            .environmentObject(LocalRuntime.shared)
            .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
        let renderer = ImageRenderer(content: view)
        renderer.scale = 1
        if let image = renderer.nsImage {
            writePNG(image, to: path)
        }
        exit(0)
    }

    private static func renderBattleScene(map: BattleMap, participants: [BattleParticipant]) -> NSImage? {
        let scene = BattleScene(size: CGSize(width: 800, height: 540))
        scene.scaleMode = .aspectFill
        scene.participants = participants
        scene.configure(map: map)
        let view = SKView(frame: NSRect(x: 0, y: 0, width: 800, height: 540))
        view.presentScene(scene)
        RunLoop.current.run(until: Date().addingTimeInterval(0.12))
        guard let texture = view.texture(
            from: scene,
            crop: CGRect(origin: .zero, size: scene.size)
        ) else { return nil }
        return NSImage(cgImage: texture.cgImage(), size: NSSize(width: 800, height: 540))
    }

    private static func writePNG(_ image: NSImage, to path: String) {
        if let tiff = image.tiffRepresentation,
           let rep = NSBitmapImageRep(data: tiff),
           let png = rep.representation(using: .png, properties: [:]) {
            try? png.write(to: URL(fileURLWithPath: path))
        }
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(session)
                .environmentObject(runtime)
                .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
        }
        .windowStyle(.titleBar)
        .windowResizability(.contentSize)
        .defaultSize(width: Theme.windowSize.width, height: Theme.windowSize.height)
    }
}
