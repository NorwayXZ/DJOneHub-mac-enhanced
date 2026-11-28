import AppKit
import CoreImage
import SwiftUI
import UniformTypeIdentifiers
import WebKit

/// An SMS a QR code asks the phone to send, such as Google's "scan this code
/// to send a text" sign-in check: smsto:<number>:<text> or
/// sms:<number>?body=<text>.
struct QRSMS: Equatable {
    var number: String
    var body: String
    /// The link as the QR code or page gave it, shown so it can be checked.
    var raw: String

    init?(_ text: String) {
        let raw = text.trimmingCharacters(in: .whitespacesAndNewlines)
        self.raw = raw
        let lower = raw.lowercased()
        if lower.hasPrefix("smsto:") || lower.hasPrefix("mmsto:") {
            let rest = String(raw.dropFirst(6))
            let parts = rest.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false).map(String.init)
            number = parts[0]
            body = parts.count > 1 ? parts[1] : ""
        } else if lower.hasPrefix("sms:") {
            // sms:<number>?body=..., but iPhone pages also write
            // sms:<number>&body=... or sms:<number>;?body=...
            var rest = String(raw.dropFirst(4))
            while rest.hasPrefix("/") { rest.removeFirst() }
            let cut = rest.firstIndex(where: { $0 == "?" || $0 == "&" || $0 == ";" }) ?? rest.endIndex
            number = String(rest[..<cut])
            body = ""
            if cut < rest.endIndex {
                for item in rest[rest.index(after: cut)...].split(whereSeparator: { $0 == "&" || $0 == "?" || $0 == ";" }) {
                    let pair = item.split(separator: "=", maxSplits: 1).map(String.init)
                    if pair.count == 2, pair[0].lowercased() == "body" {
                        body = pair[1].replacingOccurrences(of: "+", with: " ")
                    }
                }
            }
        } else {
            return nil
        }
        number = (number.removingPercentEncoding ?? number).filter { $0.isNumber || $0 == "+" }
        body = body.removingPercentEncoding ?? body
        guard number.count >= 3 else { return nil }
    }

    /// Google's QR code is not an SMS but a page for the phone to open
    /// (accounts.google.com/devicephoneverification/...), which then asks
    /// the phone to send the SMS.
    static func verifyPage(_ text: String) -> URL? {
        guard let url = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              url.scheme?.lowercased() == "https",
              let host = url.host?.lowercased(),
              host == "google.com" || host.hasSuffix(".google.com")
        else { return nil }
        return url
    }

    /// Finds an SMS QR code in an image; also returns the text of any other
    /// QR code found, to explain what it was.
    static func scan(_ image: NSImage) -> (sms: QRSMS?, other: String?) {
        guard let tiff = image.tiffRepresentation, let ci = CIImage(data: tiff),
              let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])
        else { return (nil, nil) }
        var other: String?
        for case let feature as CIQRCodeFeature in detector.features(in: ci) {
            guard let text = feature.messageString else { continue }
            if let sms = QRSMS(text) { return (sms, nil) }
            other = other ?? text
        }
        return (nil, other)
    }
}

/// 扫码发短信：识别网页上“扫码发送短信”的二维码（如谷歌登录验证），用模块里的卡发出去。
struct QRSMSSheet: View {
    @EnvironmentObject private var calls: CallCenter
    @Environment(\.dismiss) private var dismiss
    let ownNumber: String
    let capability: CardCapability?
    @State private var sms: QRSMS?
    @State private var note: (String, Bool)?
    @State private var dropTargeted = false
    @State private var capturing = false
    @State private var sending = false
    @State private var sent = false
    /// Google's verification page, opened as a phone would.
    @State private var verifyURL: URL?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(L10n.t("扫码发短信")).font(.title3.weight(.semibold))
            Text(L10n.t("网页要求“用手机扫二维码发送短信”时（如谷歌登录验证），在这里识别二维码，用模块里的卡把短信发出去，再回网页点下一步。"))
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)

            if let capability, capability.dataOnly {
                CardCapabilityBanner(capability: capability)
            }

            if let verifyURL, sms == nil {
                Text(L10n.t("这是谷歌的手机验证网页，已按手机方式打开。网页上如果有「发送短信 / Send SMS」按钮就点一下，DJOneHub 会接住这条短信，再用模块里的卡发出。"))
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                PhoneWebView(url: verifyURL) { code in
                    sms = code
                    sent = false
                    note = (L10n.t("谷歌网页给出了验证短信，确认后点「发送短信」"), true)
                }
                .frame(height: 520)
                .clipShape(RoundedRectangle(cornerRadius: 8))
                .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(.quaternary))
            } else {
                dropZone
            }

            if let note {
                Text(note.0).font(.caption).foregroundStyle(note.1 ? Color.green : Color.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let sms {
                VStack(alignment: .leading, spacing: 6) {
                    LabeledContent(L10n.t("发给")) { Text(sms.number).font(.body.monospacedDigit()).textSelection(.enabled) }
                    LabeledContent(L10n.t("内容")) {
                        TextField(L10n.t("短信内容"), text: Binding(get: { self.sms?.body ?? "" }, set: { self.sms?.body = $0 }), axis: .vertical)
                            .textFieldStyle(.roundedBorder)
                    }
                    if sms.body.isEmpty {
                        Text(L10n.t("网页没给短信内容。看一下浏览器里的谷歌页面是否写着要发送的文字，填进上面再发。"))
                            .font(.caption2).foregroundStyle(.orange)
                    }
                    LabeledContent(L10n.t("用这张卡发")) { Text(ownNumber.isEmpty ? L10n.t("左上角选中的模块") : ownNumber).font(.body.monospacedDigit()) }
                    Text(L10n.t("原始链接：") + sms.raw).font(.caption2).foregroundStyle(.secondary).textSelection(.enabled).lineLimit(3)
                    if sms.number.hasPrefix("+") || sms.number.hasPrefix("00") {
                        Text(L10n.t("这是国际短信，运营商可能按国际短信收费（通常几毛钱到一两块）。"))
                            .font(.caption2).foregroundStyle(.secondary)
                    }
                }
                .padding(10)
                .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
            }

            HStack {
                if sending {
                    ProgressView().controlSize(.small)
                    Text(L10n.t("正在发送…")).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button(sent ? L10n.t("完成") : L10n.t("取消")) { dismiss() }
                if !sent {
                    Button(L10n.t("发送短信")) { Task { await send() } }
                        .buttonStyle(.borderedProminent)
                        .disabled(sms == nil || sending)
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .padding(20)
        .frame(width: verifyURL != nil && sms == nil ? 460 : 440)
        .onAppear { readPasteboard(quiet: true) }
    }

    private var dropZone: some View {
        VStack(spacing: 6) {
            Image(systemName: "qrcode.viewfinder").font(.system(size: 26)).foregroundStyle(.secondary)
            Button {
                Task { await captureScreen() }
            } label: {
                Label(capturing ? L10n.t("请在屏幕上框选二维码…") : L10n.t("框选屏幕上的二维码"), systemImage: "viewfinder")
            }
            .buttonStyle(.borderedProminent)
            .disabled(capturing)
            HStack(spacing: 8) {
                Button(L10n.t("粘贴截图")) { readPasteboard(quiet: false) }.controlSize(.small)
                Button(L10n.t("选择图片…")) { pickImage() }.controlSize(.small)
            }
            Text(L10n.t("也可以把二维码图片拖到这里")).font(.caption2).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 8).strokeBorder(dropTargeted ? Color.accentColor : Color.secondary.opacity(0.4), style: StrokeStyle(lineWidth: 1.5, dash: [5])))
        .onDrop(of: [.fileURL, .image, .plainText], isTargeted: $dropTargeted) { providers in
            handleDrop(providers)
            return true
        }
    }

    private func use(image: NSImage?, source: String) {
        guard let image else { note = (L10n.t("打不开这张图片"), false); return }
        let found = QRSMS.scan(image)
        if let code = found.sms {
            sms = code
            sent = false
            note = (L10n.t("已从\(source)识别出短信二维码"), true)
        } else if let other = found.other, let page = QRSMS.verifyPage(other) {
            openVerifyPage(page)
        } else if let other = found.other {
            note = (L10n.t("找到了二维码，但它不是“发送短信”的二维码：") + String(other.prefix(80)), false)
        } else {
            note = (L10n.t("没找到二维码。框选时把整个二维码框进去，或换一张清晰的截图。"), false)
        }
    }

    /// The system's own region screenshot (the same as ⌘⇧4), copied to the
    /// clipboard; this window steps aside so it does not cover the web page.
    private func captureScreen() async {
        capturing = true
        let window = NSApp.keyWindow
        let board = NSPasteboard.general
        let before = board.changeCount
        window?.orderOut(nil)
        NSApp.mainWindow?.orderOut(nil)
        await Task.detached {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
            process.arguments = ["-i", "-c", "-x"]
            try? process.run()
            process.waitUntilExit()
        }.value
        NSApp.activate(ignoringOtherApps: true)
        NSApp.mainWindow?.makeKeyAndOrderFront(nil)
        window?.makeKeyAndOrderFront(nil)
        capturing = false
        if board.changeCount == before {
            note = (L10n.t("没有截到图（按了 Esc，或系统没允许截屏）。也可以按 ⌘⇧⌃4 框选二维码后点「粘贴截图」。"), false)
            return
        }
        use(image: NSImage(pasteboard: board), source: L10n.t("屏幕"))
    }

    private func openVerifyPage(_ page: URL) {
        sms = nil
        sent = false
        verifyURL = page
        note = nil
    }

    private func readPasteboard(quiet: Bool) {
        let board = NSPasteboard.general
        if let text = board.string(forType: .string), let code = QRSMS(text) {
            sms = code
            note = (L10n.t("已从剪贴板文字识别出短信二维码"), true)
        } else if !quiet, let text = board.string(forType: .string), let page = QRSMS.verifyPage(text) {
            openVerifyPage(page)
        } else if let image = NSImage(pasteboard: board), let found = Optional(QRSMS.scan(image)), found.sms != nil || (!quiet && found.other.flatMap(QRSMS.verifyPage) != nil) {
            if let code = found.sms {
                sms = code
                note = (L10n.t("已从剪贴板截图识别出短信二维码"), true)
            } else if let page = found.other.flatMap(QRSMS.verifyPage) {
                openVerifyPage(page)
            }
        } else if !quiet {
            note = (L10n.t("剪贴板里没有短信二维码。先按 ⌘⇧⌃4 框选二维码，再点「粘贴截图」。"), false)
        }
    }

    private func pickImage() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.image]
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        use(image: NSImage(contentsOf: url), source: L10n.t("图片"))
    }

    private func handleDrop(_ providers: [NSItemProvider]) {
        guard let provider = providers.first else { return }
        if provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
            _ = provider.loadObject(ofClass: URL.self) { url, _ in
                let image = url.flatMap { NSImage(contentsOf: $0) }
                DispatchQueue.main.async { use(image: image, source: L10n.t("图片")) }
            }
        } else if provider.canLoadObject(ofClass: NSImage.self) {
            _ = provider.loadObject(ofClass: NSImage.self) { image, _ in
                DispatchQueue.main.async { use(image: image as? NSImage, source: L10n.t("图片")) }
            }
        } else {
            _ = provider.loadObject(ofClass: String.self) { text, _ in
                DispatchQueue.main.async {
                    if let text, let code = QRSMS(text) { sms = code; note = (L10n.t("已识别"), true) }
                    else { note = (L10n.t("不是“发送短信”的二维码内容。"), false) }
                }
            }
        }
    }

    private func send() async {
        guard let sms else { return }
        sending = true
        defer { sending = false }
        do {
            _ = try await calls.apiClient.sendSMS(to: sms.number, message: sms.body)
            sent = true
            note = (L10n.t("短信已发出。回到网页点「下一步 / 我已发送」，谷歌通常几秒到一分钟内确认。"), true)
            Toast.shared.show(L10n.t("验证短信已发出，回网页点下一步"), .success)
        } catch {
            note = (L10n.t("发送失败：") + error.localizedDescription, false)
        }
    }
}

/// A web page shown as an iPhone would see it. When the page asks to send an
/// SMS (an sms: link), the SMS is handed back instead of leaving the app.
struct PhoneWebView: NSViewRepresentable {
    let url: URL
    let onSMS: (QRSMS) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(onSMS: onSMS) }

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        let view = WKWebView(frame: .zero, configuration: config)
        view.customUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
        view.navigationDelegate = context.coordinator
        view.uiDelegate = context.coordinator
        view.load(URLRequest(url: url))
        return view
    }

    func updateNSView(_ view: WKWebView, context: Context) {}

    final class Coordinator: NSObject, WKNavigationDelegate, WKUIDelegate {
        let onSMS: (QRSMS) -> Void
        init(onSMS: @escaping (QRSMS) -> Void) { self.onSMS = onSMS }

        private func take(_ url: URL?) -> Bool {
            guard let url, let scheme = url.scheme?.lowercased(),
                  ["sms", "smsto", "mmsto"].contains(scheme) else { return false }
            if let code = QRSMS(url.absoluteString) {
                DispatchQueue.main.async { self.onSMS(code) }
            }
            return true
        }

        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
            decisionHandler(take(action.request.url) ? .cancel : .allow)
        }

        // Links that open a new window (target=_blank, window.open).
        func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
            if !take(action.request.url), let url = action.request.url {
                webView.load(URLRequest(url: url))
            }
            return nil
        }
    }
}
