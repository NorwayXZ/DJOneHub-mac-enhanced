import AppKit
import CoreImage
import SwiftUI
import UniformTypeIdentifiers

/// An eSIM activation code (the text inside the QR code):
/// LPA:1$<SM-DP+ address>$<Matching ID>[$<OID>[$<confirmation code flag>]].
struct ActivationCode: Equatable {
    var smdp: String
    var matchingID: String
    var needsConfirmationCode: Bool

    init?(_ text: String) {
        var code = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if let range = code.range(of: "LPA:", options: [.caseInsensitive, .anchored]) { code.removeSubrange(range) }
        let parts = code.components(separatedBy: "$")
        guard parts.count >= 2, parts[0] == "1" else { return nil }
        let smdp = parts[1].trimmingCharacters(in: .whitespaces)
        guard !smdp.isEmpty, smdp.contains(".") else { return nil }
        self.smdp = smdp
        matchingID = parts.count > 2 ? parts[2].trimmingCharacters(in: .whitespaces) : ""
        needsConfirmationCode = parts.count > 4 && parts[4].trimmingCharacters(in: .whitespaces) == "1"
    }

    /// Finds an activation code in an image (a QR code screenshot or photo).
    static func scan(_ image: NSImage) -> ActivationCode? {
        guard let tiff = image.tiffRepresentation, let ci = CIImage(data: tiff),
              let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])
        else { return nil }
        for case let feature as CIQRCodeFeature in detector.features(in: ci) {
            if let text = feature.messageString, let code = ActivationCode(text) { return code }
        }
        return nil
    }
}

/// 下载新的 eSIM Profile：拖入二维码图片、粘贴 LPA:1$… 文字，或手动填写。
struct ESIMDownloadSheet: View {
    @Environment(\.dismiss) private var dismiss
    /// Downloads the Profile and returns the result text.
    let onDownload: (String, String, String, String, String) async throws -> String
    @State private var smdp = ""
    @State private var matchingID = ""
    @State private var confirmationCode = ""
    @State private var needsConfirmation = false
    @State private var imei = ""
    @State private var aid = ""
    @State private var showAdvanced = false
    @State private var dropTargeted = false
    @State private var note: (String, Bool)?
    @State private var downloading = false
    @State private var started: Date?

    private var canSubmit: Bool {
        !downloading && !smdp.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && (!needsConfirmation || !confirmationCode.trimmingCharacters(in: .whitespaces).isEmpty)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(L10n.t("下载新的 Profile")).font(.title3.weight(.semibold))
            Text(L10n.t("从运营商服务器下载新的 eSIM 号码并写入卡里，不需要手机或 XeSIM App。写入约需 1–5 分钟，期间请勿拔出模块。"))
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            dropZone
            if let (text, ok) = note {
                Text(text).font(.caption).foregroundStyle(ok ? Color.green : Color.orange).fixedSize(horizontal: false, vertical: true)
            }
            TextField(L10n.t("SM-DP+ 地址"), text: $smdp).textFieldStyle(.roundedBorder)
            TextField(L10n.t("激活码 / Matching ID"), text: $matchingID).textFieldStyle(.roundedBorder)
            TextField(needsConfirmation ? L10n.t("确认码（这个号码要求填写）") : L10n.t("确认码（一般不用填）"), text: $confirmationCode)
                .textFieldStyle(.roundedBorder)
            DisclosureGroup(L10n.t("高级"), isExpanded: $showAdvanced) {
                VStack(spacing: 8) {
                    TextField(L10n.t("IMEI（留空自动读取模块的）"), text: $imei).textFieldStyle(.roundedBorder)
                    TextField(L10n.t("AID（留空自动）"), text: $aid).textFieldStyle(.roundedBorder)
                }.padding(.top, 6)
            }.font(.caption)
            if downloading {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    TimelineView(.periodic(from: .now, by: 1)) { context in
                        Text(L10n.t("正在下载并写入…") + " \(Int(context.date.timeIntervalSince(started ?? context.date))) 秒")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            HStack(spacing: 10) {
                Spacer()
                Button(downloading ? L10n.t("关闭") : L10n.t("取消")) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button { Task { await download() } } label: {
                    Text(L10n.t("开始下载")).frame(minWidth: 70)
                }
                .buttonStyle(.borderedProminent).disabled(!canSubmit)
            }
        }
        .frame(width: 380)
        .padding(20)
        .onAppear { readPasteboard(quiet: true) }
    }

    private var dropZone: some View {
        VStack(spacing: 6) {
            Image(systemName: "qrcode.viewfinder").font(.system(size: 26)).foregroundStyle(.secondary)
            Text(L10n.t("把二维码图片拖到这里")).font(.system(size: 12, weight: .medium))
            HStack(spacing: 8) {
                Button(L10n.t("选择图片…")) { pickImage() }.controlSize(.small)
                Button(L10n.t("粘贴二维码或 LPA 文字")) { readPasteboard(quiet: false) }.controlSize(.small)
            }
        }
        .frame(maxWidth: .infinity).padding(.vertical, 12)
        .background(RoundedRectangle(cornerRadius: 8).strokeBorder(dropTargeted ? Color.accentColor : Color.secondary.opacity(0.4), style: StrokeStyle(lineWidth: 1.5, dash: [5])))
        .onDrop(of: [.fileURL, .image, .plainText], isTargeted: $dropTargeted) { providers in
            handleDrop(providers)
            return true
        }
    }

    private func fill(_ code: ActivationCode, from source: String) {
        smdp = code.smdp
        matchingID = code.matchingID
        needsConfirmation = code.needsConfirmationCode
        note = (L10n.t("已从\(source)识别：") + code.smdp + (code.needsConfirmationCode ? L10n.t("（还需要填确认码）") : ""), true)
    }

    private func use(image: NSImage?, source: String) {
        guard let image else { note = (L10n.t("打不开这张图片"), false); return }
        if let code = ActivationCode.scan(image) { fill(code, from: source) }
        else { note = (L10n.t("图片里没找到 eSIM 二维码。请换一张清晰的截图，或直接粘贴 LPA:1$… 文字。"), false) }
    }

    private func readPasteboard(quiet: Bool) {
        let board = NSPasteboard.general
        if let text = board.string(forType: .string), let code = ActivationCode(text) {
            fill(code, from: L10n.t("剪贴板文字"))
        } else if let image = NSImage(pasteboard: board), let code = ActivationCode.scan(image) {
            fill(code, from: L10n.t("剪贴板图片"))
        } else if !quiet {
            note = (L10n.t("剪贴板里没有 eSIM 二维码或 LPA:1$… 文字。"), false)
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
                    if let text, let code = ActivationCode(text) { fill(code, from: L10n.t("文字")) }
                    else { note = (L10n.t("不是有效的 eSIM 激活码。"), false) }
                }
            }
        }
    }

    private func download() async {
        downloading = true
        started = Date()
        note = nil
        defer { downloading = false }
        let trim = { (s: String) in s.trimmingCharacters(in: .whitespacesAndNewlines) }
        do {
            let result = try await onDownload(trim(smdp), trim(matchingID), trim(confirmationCode), trim(imei), trim(aid))
            note = ("✅ " + result, true)
            try? await Task.sleep(for: .seconds(2))
            dismiss()
        } catch {
            note = (error.localizedDescription, false)
        }
    }
}
