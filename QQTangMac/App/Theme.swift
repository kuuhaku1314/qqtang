import SwiftUI

enum Theme {
    // 原版客户端固定 800×600 窗口
    static let windowSize = CGSize(width: 800, height: 600)

    static let candyPink = Color(red: 1.00, green: 0.62, blue: 0.72)
    static let deepPink = Color(red: 0.86, green: 0.22, blue: 0.42)
    static let cream = Color(red: 1.00, green: 0.96, blue: 0.88)
    static let panel = Color(red: 1.00, green: 0.93, blue: 0.78)
    static let orange = Color(red: 0.98, green: 0.45, blue: 0.16)
    static let gold = Color(red: 0.98, green: 0.78, blue: 0.22)
    static let sky = Color(red: 0.42, green: 0.72, blue: 0.96)
    static let ink = Color(red: 0.28, green: 0.16, blue: 0.18)
    static let seatEmpty = Color(red: 0.93, green: 0.86, blue: 0.74)
    static let teamRed = Color(red: 0.92, green: 0.28, blue: 0.28)
    static let teamBlue = Color(red: 0.22, green: 0.48, blue: 0.92)
}

struct CandyPanel<Content: View>: View {
    var title: String?
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let title {
                Text(title)
                    .font(.system(size: 15, weight: .heavy, design: .rounded))
                    .foregroundStyle(Theme.deepPink)
            }
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Theme.panel)
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 14, style: .continuous)
                .stroke(Theme.deepPink.opacity(0.25), lineWidth: 2)
        )
    }
}

struct CandyButton: View {
    let title: String
    var tint: Color = Theme.orange
    var enabled: Bool = true
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
                .font(.system(size: 15, weight: .heavy, design: .rounded))
                .foregroundStyle(.white)
                .padding(.horizontal, 18)
                .padding(.vertical, 8)
                .background(enabled ? tint : Color.gray.opacity(0.45))
                .clipShape(Capsule())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
    }
}
