import AppKit
import Foundation

/// DMG 里的 DJOneHub.app 内置后端运行时（Contents/Resources/djonehub）和 install.sh。
/// 从「应用程序」或磁盘映像打开它时，它只是安装入口：首次打开或版本更新时把运行时
/// 和 App 装到用户目录并注册开机自启；已是最新时直接唤起已安装的副本。
/// 已安装副本、更新包里的 App 与开发构建都不走这里。
enum AppInstaller {
    static let installedApp = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/DJOneHub/notifier/DJOneHubNotifier.app")

    /// 返回 true 表示本进程作为安装入口运行完毕，不再启动通知助手界面。
    @MainActor
    static func runIfLauncher() -> Bool {
        let bundle = Bundle.main.bundleURL.standardizedFileURL
        guard bundle.path != installedApp.standardizedFileURL.path,
              let script = Bundle.main.url(forResource: "install", withExtension: "sh"),
              FileManager.default.isExecutableFile(atPath: bundle.appendingPathComponent("Contents/Resources/djonehub/bin/djonehub-macos").path)
        else { return false }
        let app = NSApplication.shared
        let delegate = LauncherDelegate(bundle: bundle, script: script)
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        app.run()
        withExtendedLifetime(delegate) {}
        return true
    }

    static func buildNumber(of app: URL) -> Int? {
        guard let data = try? Data(contentsOf: app.appendingPathComponent("Contents/Info.plist")),
              let info = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
              let value = info["CFBundleVersion"] as? String
        else { return nil }
        return Int(value)
    }

    /// 已安装副本缺失、比本入口旧，或开机自启被删掉时需要（重新）安装。
    static func needsInstall(launcher: URL) -> Bool {
        let agent = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/com.jamie.djonehub.plist")
        guard FileManager.default.fileExists(atPath: agent.path),
              let installed = buildNumber(of: installedApp) else { return true }
        return installed < (buildNumber(of: launcher) ?? 0)
    }

    /// 直接从磁盘映像打开（或被系统随机化路径隔离运行）时，顺手把自己放进「应用程序」，
    /// 这样以后从启动台就能打开。拷贝失败不影响安装。
    static func copyToApplicationsIfNeeded(_ bundle: URL) {
        let path = bundle.path
        guard path.hasPrefix("/Volumes/") || path.contains("/AppTranslocation/") else { return }
        let target = URL(fileURLWithPath: "/Applications/DJOneHub.app")
        guard FileManager.default.isWritableFile(atPath: "/Applications") else { return }
        let staged = URL(fileURLWithPath: "/Applications/.DJOneHub.app.installing")
        try? FileManager.default.removeItem(at: staged)
        let ditto = Process()
        ditto.executableURL = URL(fileURLWithPath: "/usr/bin/ditto")
        ditto.arguments = ["--noqtn", bundle.path, staged.path]
        guard (try? ditto.run()) != nil else { return }
        ditto.waitUntilExit()
        guard ditto.terminationStatus == 0 else { try? FileManager.default.removeItem(at: staged); return }
        try? FileManager.default.removeItem(at: target)
        try? FileManager.default.moveItem(at: staged, to: target)
    }

    /// 早期版本会把通知助手直接拷进「应用程序」；同一应用标识的旧副本会在启动台里重复出现，
    /// 打开还会跑旧界面，安装时移到废纸篓。
    static func trashLegacyApplicationsCopy() {
        let legacy = URL(fileURLWithPath: "/Applications/DJOneHubNotifier.app")
        guard let data = try? Data(contentsOf: legacy.appendingPathComponent("Contents/Info.plist")),
              let info = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
              info["CFBundleIdentifier"] as? String == "com.jamie.djonehub.notifier"
        else { return }
        try? FileManager.default.trashItem(at: legacy, resultingItemURL: nil)
    }

    static func openInstalledApp() {
        let open = Process()
        open.executableURL = URL(fileURLWithPath: "/usr/bin/open")
        open.arguments = [installedApp.path, "--args", "--show-window"]
        try? open.run()
        open.waitUntilExit()
    }
}

@MainActor
private final class LauncherDelegate: NSObject, NSApplicationDelegate {
    private let bundle: URL
    private let script: URL
    private var window: NSWindow?

    init(bundle: URL, script: URL) {
        self.bundle = bundle
        self.script = script
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard AppInstaller.needsInstall(launcher: bundle) else {
            AppInstaller.openInstalledApp()
            NSApp.terminate(nil)
            return
        }
        showProgress()
        let bundle = bundle, script = script
        Task.detached {
            AppInstaller.copyToApplicationsIfNeeded(bundle)
            AppInstaller.trashLegacyApplicationsCopy()
            let result = Self.runInstaller(script: script, app: bundle)
            await MainActor.run {
                if let failure = result {
                    self.window?.orderOut(nil)
                    let alert = NSAlert()
                    alert.alertStyle = .warning
                    alert.messageText = "DJOneHub 安装未完成"
                    alert.informativeText = failure
                    alert.runModal()
                }
                NSApp.terminate(nil)
            }
        }
    }

    /// 成功返回 nil，失败返回给用户看的原因（安装日志末尾几行）。
    nonisolated private static func runInstaller(script: URL, app: URL) -> String? {
        let logs = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/DJOneHub")
        try? FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
        let log = logs.appendingPathComponent("install.log")
        FileManager.default.createFile(atPath: log.path, contents: nil)
        guard let handle = try? FileHandle(forWritingTo: log) else { return "无法写入安装日志：\(log.path)" }
        defer { try? handle.close() }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/zsh")
        process.arguments = [script.path, app.path]
        process.standardOutput = handle
        process.standardError = handle
        do { try process.run() } catch { return error.localizedDescription }
        process.waitUntilExit()
        guard process.terminationStatus != 0 else { return nil }
        let tail = (try? String(contentsOf: log, encoding: .utf8))?
            .split(separator: "\n").suffix(4).joined(separator: "\n") ?? ""
        return "\(tail)\n\n完整日志：\(log.path)"
    }

    private func showProgress() {
        let spinner = NSProgressIndicator()
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.startAnimation(nil)
        let title = NSTextField(labelWithString: "正在安装 DJOneHub…")
        title.font = .boldSystemFont(ofSize: 13)
        let detail = NSTextField(labelWithString: "安装后台服务并设置开机自启，完成后自动打开控制台。")
        detail.textColor = .secondaryLabelColor
        let text = NSStackView(views: [title, detail])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 4
        let row = NSStackView(views: [spinner, text])
        row.alignment = .centerY
        row.spacing = 12
        row.edgeInsets = NSEdgeInsets(top: 20, left: 20, bottom: 20, right: 24)
        let window = NSWindow(contentRect: .zero, styleMask: [.titled], backing: .buffered, defer: false)
        window.title = "DJOneHub"
        window.contentView = row
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        self.window = window
    }
}
