import AppKit
import SwiftUI

/// QQ堂 3.0「盛夏之约」登录界面的原始拆分素材。
///
/// 素材放在应用包的 `Legacy30` 文件夹中；源码目录回退只用于 Xcode 预览和
/// 命令行离屏渲染。
enum Legacy30Assets {
    private static var cache: [String: NSImage] = [:]

    static func image(_ relativePath: String) -> NSImage? {
        if let cached = cache[relativePath] {
            return cached
        }

        for root in roots {
            let url = root.appendingPathComponent(relativePath)
            if let image = NSImage(contentsOf: url) {
                cache[relativePath] = image
                return image
            }
        }
        return nil
    }

    private static var roots: [URL] {
        var values: [URL] = []
        if let resourceURL = Bundle.main.resourceURL {
            values.append(resourceURL.appendingPathComponent("Legacy30", isDirectory: true))
        }

        let sourceRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("Resources/Legacy30", isDirectory: true)
        values.append(sourceRoot)
        return values
    }
}

struct Legacy30Image: View {
    let path: String

    var body: some View {
        if let image = Legacy30Assets.image(path) {
            Image(nsImage: image)
                .resizable()
                .interpolation(.high)
        } else {
            Color.clear
        }
    }
}
