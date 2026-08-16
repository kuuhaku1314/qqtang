import AppKit
import SwiftUI

/// Bundled QQ堂 4.3 UI material.
///
/// The release build reads only the versioned `Legacy43` resource folder.
/// The source-tree fallback keeps previews and the command-line snapshot mode
/// working before Xcode has copied resources into an app bundle.
enum Legacy43Assets {
    private static var cache: [String: NSImage] = [:]

    static func fileURL(_ relativePath: String) -> URL? {
        roots
            .map { $0.appendingPathComponent(relativePath) }
            .first { FileManager.default.fileExists(atPath: $0.path) }
    }

    static func image(_ relativePath: String) -> NSImage? {
        if let cached = cache[relativePath] {
            return cached
        }

        if let url = fileURL(relativePath),
           let image = NSImage(contentsOf: url)
        {
            cache[relativePath] = image
            return image
        }
        return nil
    }

    static func pixelSize(_ relativePath: String) -> CGSize? {
        image(relativePath)?.size
    }

    private static var roots: [URL] {
        var values: [URL] = []
        if let resourceURL = Bundle.main.resourceURL {
            values.append(resourceURL.appendingPathComponent("Legacy43", isDirectory: true))
        }
        values.append(
            URL(fileURLWithPath: #filePath)
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .appendingPathComponent("Resources/Legacy43", isDirectory: true)
        )
        return values
    }
}

struct Legacy43Image: View {
    let path: String
    var interpolation: Image.Interpolation = .high

    var body: some View {
        if let image = Legacy43Assets.image(path) {
            Image(nsImage: image)
                .resizable()
                .interpolation(interpolation)
        } else {
            Color.clear
        }
    }
}

/// Original bitmap-button behavior: state 0 is pressed, 1 hovered and 2/3 normal.
struct Legacy43Button: View {
    let normal: String
    let hovered: String
    let pressed: String
    let size: CGSize
    var enabled = true
    var sound: LegacySound = .normal
    let action: () -> Void

    @State private var isHovered = false

    var body: some View {
        Button {
            SoundPlayer.play(sound)
            action()
        } label: {
            Color.clear
                .frame(width: size.width, height: size.height)
                .contentShape(Rectangle())
        }
        .buttonStyle(
            Legacy43ButtonStyle(
                normal: normal,
                hovered: hovered,
                pressed: pressed,
                size: size,
                isHovered: isHovered,
                enabled: enabled
            )
        )
        .disabled(!enabled)
        .onHover { isHovered = $0 && enabled }
    }
}

private struct Legacy43ButtonStyle: ButtonStyle {
    let normal: String
    let hovered: String
    let pressed: String
    let size: CGSize
    let isHovered: Bool
    let enabled: Bool

    func makeBody(configuration: Configuration) -> some View {
        Legacy43Image(
            path: configuration.isPressed
                ? pressed
                : (isHovered ? hovered : normal)
        )
        .frame(width: size.width, height: size.height)
        .opacity(enabled ? 1 : 0.55)
        .contentShape(Rectangle())
    }
}
