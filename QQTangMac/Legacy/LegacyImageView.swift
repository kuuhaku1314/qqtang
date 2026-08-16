import AppKit
import SwiftUI

struct LegacyImageView: View {
    let image: NSImage?

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.medium)
            } else {
                LinearGradient(
                    colors: [Theme.candyPink, Theme.gold.opacity(0.85)],
                    startPoint: .topLeading,
                    endPoint: .bottomTrailing
                )
            }
        }
    }
}
