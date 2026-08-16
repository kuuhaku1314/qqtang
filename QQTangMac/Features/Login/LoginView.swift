import AppKit
import SwiftUI

/// QQ堂 4.3《欢聚一堂》登录页。
///
/// 布局来自客户端 `uiLogin.pyc`：所有坐标均是原版 800×600 逻辑像素。
/// 背景、4.3 Logo、3.2 后沿用的登录框及按钮都使用历史客户端原图。
struct LoginView: View {
    @EnvironmentObject private var session: GameSession
    @State private var agreedToTerms = true
    @State private var waitSeconds = 30
    @FocusState private var focusedField: Field?

    var snapshotMode = false

    private enum Field {
        case account
        case password
    }

    private static let canvas = CGSize(width: 800, height: 600)
    private let waitTimer = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        ZStack(alignment: .topLeading) {
            Legacy43Image(path: "Login/background.png")
                .frame(width: 800, height: 600)

            Legacy43Image(path: "Login/dialog.png")
                .frame(width: 346, height: 429)
                .offset(x: 437, y: 112)

            Legacy43Image(path: "Login/logo.png")
                .frame(width: 199, height: 167)
                .offset(x: 504, y: 18)

            accountField
                .offset(x: 576, y: 216)

            passwordField
                .offset(x: 576, y: 259)

            termsControl
                .offset(x: 587, y: 297)

            Legacy43Button(
                normal: "Login/playAnim-2.png",
                hovered: "Login/playAnim-1.png",
                pressed: "Login/playAnim-0.png",
                size: CGSize(width: 98, height: 31)
            ) {
                session.loginError = "当前 4.3 本地资源包不含开场动画。"
            }
            .offset(x: 475, y: 321)

            Legacy43Button(
                normal: "Login/help-3.png",
                hovered: "Login/help-2.png",
                pressed: "Login/help-0.png",
                size: CGSize(width: 76, height: 31)
            ) {
                if let url = URL(string: "https://github.com/kuuhaku1314/qqtang") {
                    NSWorkspace.shared.open(url)
                }
            }
            .offset(x: 466, y: 371)

            Legacy43Button(
                normal: "Login/login-2.png",
                hovered: "Login/login-1.png",
                pressed: "Login/login-0.png",
                size: CGSize(width: 80, height: 31),
                enabled: !session.isLoggingIn
            ) {
                submit()
            }
            .offset(x: 624, y: 371)

            Legacy43Button(
                normal: "Login/quit-2.png",
                hovered: "Login/quit-1.png",
                pressed: "Login/quit-0.png",
                size: CGSize(width: 41, height: 31)
            ) {
                NSApplication.shared.terminate(nil)
            }
            .offset(x: 709, y: 371)

            if session.isLoggingIn {
                waitOverlay
            }

            if let error = session.loginError {
                messageBox(error)
            }
        }
        .frame(width: Self.canvas.width, height: Self.canvas.height, alignment: .topLeading)
        .scaleEffect(Theme.windowSize.width / Self.canvas.width)
        .frame(width: Theme.windowSize.width, height: Theme.windowSize.height)
        .clipped()
        .onAppear { focusedField = .password }
        .onExitCommand { NSApplication.shared.terminate(nil) }
        .onChange(of: session.isLoggingIn) { _, started in
            if started { waitSeconds = 30 }
        }
        .onReceive(waitTimer) { _ in
            if session.isLoggingIn, waitSeconds > 0 {
                waitSeconds -= 1
            }
        }
    }

    @ViewBuilder
    private var accountField: some View {
        if snapshotMode {
            fieldText(session.loginUID)
        } else {
            TextField("", text: $session.loginUID)
                .focused($focusedField, equals: .account)
                .onSubmit { focusedField = .password }
                .modifier(Legacy43FieldStyle())
        }
    }

    @ViewBuilder
    private var passwordField: some View {
        if snapshotMode {
            fieldText(String(repeating: "●", count: min(max(session.loginPassword.count, 1), 8)))
        } else {
            SecureField("", text: $session.loginPassword)
                .focused($focusedField, equals: .password)
                .onSubmit { submit() }
                .modifier(Legacy43FieldStyle())
        }
    }

    private func fieldText(_ text: String) -> some View {
        Text(text).modifier(Legacy43FieldStyle())
    }

    private var termsControl: some View {
        HStack(spacing: -8) {
            Button {
                agreedToTerms.toggle()
            } label: {
                Legacy43Image(path: agreedToTerms ? "Login/check-on.png" : "Login/check-off.png")
                    .frame(width: 20, height: 17)
            }
            .buttonStyle(.plain)

            Button {
                if let url = URL(string: "https://game.qq.com/contract.shtml") {
                    NSWorkspace.shared.open(url)
                }
            } label: {
                Legacy43Image(path: "Login/agreement.png")
                    .frame(width: 160, height: 18)
            }
            .buttonStyle(.plain)
        }
        .frame(height: 18, alignment: .leading)
    }

    private func submit() {
        guard agreedToTerms else {
            session.loginError = "请先阅读并同意游戏协议"
            return
        }
        session.login()
    }

    private var waitOverlay: some View {
        let tens = max(0, min(9, waitSeconds / 10))
        let ones = max(0, min(9, waitSeconds % 10))

        return ZStack(alignment: .topLeading) {
            Color.black.opacity(0.48)
                .frame(width: 800, height: 600)

            Legacy43Image(path: "Common/Numbers/\(tens).png")
                .frame(width: 103, height: 130)
                .offset(x: 292, y: 190)

            Legacy43Image(path: "Common/Numbers/\(ones).png")
                .frame(width: 103, height: 130)
                .offset(x: 395, y: 190)

            Legacy43Image(path: "Login/wait.png")
                .frame(width: 226, height: 95)
                .offset(x: 287, y: 320)

            Legacy43Image(path: "Login/link.gif")
                .frame(width: 184, height: 20)
                .offset(x: 308, y: 350)

            Legacy43Button(
                normal: "Common/cancel-normal.png",
                hovered: "Common/cancel-hover.png",
                pressed: "Common/cancel-pressed.png",
                size: CGSize(width: 44, height: 31)
            ) {
                session.cancelLogin()
            }
            .offset(x: 378, y: 377)
        }
    }

    private func messageBox(_ text: String) -> some View {
        ZStack(alignment: .topLeading) {
            Color.black.opacity(0.45)
                .frame(width: 800, height: 600)

            Legacy43Image(path: "Common/message-box.png")
                .frame(width: 368, height: 326)
                .offset(x: 216, y: 137)

            Text(text)
                .font(.system(size: 15, weight: .bold))
                .foregroundStyle(.white)
                .shadow(color: .black.opacity(0.7), radius: 1, x: 1, y: 1)
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
}

private struct Legacy43FieldStyle: ViewModifier {
    func body(content: Content) -> some View {
        content
            .textFieldStyle(.plain)
            .font(.system(size: 14, weight: .semibold))
            .foregroundStyle(.white)
            .shadow(color: Color(red: 0.04, green: 0.2, blue: 0.38), radius: 1, x: 1, y: 1)
            .padding(.horizontal, 4)
            .frame(width: 140, height: 24, alignment: .leading)
            .background(Color.clear)
    }
}
