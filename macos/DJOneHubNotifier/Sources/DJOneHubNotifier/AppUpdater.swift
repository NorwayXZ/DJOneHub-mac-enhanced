import AppKit
import CryptoKit
import Foundation
import SwiftUI

struct UpdateRelease: Decodable {
    let tag_name: String
    let body: String?
    let draft: Bool
    let prerelease: Bool
    let assets: [Asset]
    struct Asset: Decodable {
        let name: String
        let browser_download_url: URL
        let size: Int64
        let digest: String?
    }
}

@MainActor
final class AppUpdater: ObservableObject {
    static let repository = "NorwayXZ/DJOneHub-mac-enhanced"
    static let assetName = "DJOneHub-macOS-universal-update.zip"
    @Published var release: UpdateRelease?
    @Published var message = "手动检查 GitHub Releases，不读取源码提交作为版本。"
    @Published var busy = false
    @Published var ready = false
    @Published var progress: String = ""
    var currentVersion: String { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "dev" }
    private var staged: URL?
    private let api: DJOneHubAPI
    init(api: DJOneHubAPI) { self.api = api }
    enum Failure: LocalizedError {
        case reason(String)
        var errorDescription: String? { if case .reason(let s) = self { return s }; return nil }
    }
    func check() async {
        guard !busy else { return }
        busy = true; ready = false; release = nil; staged = nil
        defer { busy = false }
        do {
            var request = URLRequest(url: URL(string: "https://api.github.com/repos/\(Self.repository)/releases?per_page=30")!)
            request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
            request.setValue("DJOneHub-Updater", forHTTPHeaderField: "User-Agent")
            request.timeoutInterval = 20
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let response = response as? HTTPURLResponse, response.statusCode == 200 else { throw Failure.reason("GitHub 暂不可用或请求受限，请稍后重试。") }
            let releases = try JSONDecoder().decode([UpdateRelease].self, from: data)
            let candidates = releases.filter { !$0.draft && Self.newer($0.tag_name, than: currentVersion) }
            release = candidates.first { item in item.assets.contains { $0.name == Self.assetName } }
            if let release {
                message = "发现 \(release.tag_name)，可下载校验后更新。"
            } else if candidates.isEmpty {
                message = releases.isEmpty ? "此仓库尚未发布 Release。源码推送不会触发 App 更新。" : "未发现更新版本。"
            } else {
                message = "有新 Release，但缺少兼容的一键更新包。"
            }
        } catch { message = error.localizedDescription }
    }
    // Numeric core and prerelease ordering, never lexicographic tag comparison.
    nonisolated static func newer(_ tag: String, than current: String) -> Bool {
        func split(_ s: String) -> ([Int], String)? {
            let value = s.hasPrefix("v") ? String(s.dropFirst()) : s
            let bits = value.split(separator: "-", maxSplits: 1)
            let fields = bits[0].split(separator: ".", omittingEmptySubsequences: false)
            guard fields.count == 3, fields.allSatisfy({ !$0.isEmpty && $0.allSatisfy({ $0.isASCII && $0.isNumber }) }) else { return nil }
            let core = fields.compactMap { Int($0) }
            guard core.count == 3 else { return nil }
            return (core, bits.count == 2 ? String(bits[1]) : "")
        }
        guard let a = split(tag), let b = split(current) else { return false }
        if a.0 != b.0 { return b.0.lexicographicallyPrecedes(a.0) }
        if a.1 == b.1 { return false }
        if a.1.isEmpty { return true }
        if b.1.isEmpty { return false }
        return b.1.compare(a.1, options: .numeric) == .orderedAscending
    }
    func download() async {
        guard !busy, let release, let asset = release.assets.first(where: { $0.name == Self.assetName }) else { return }
        busy = true; ready = false; progress = "正在下载更新包…"
        defer { busy = false; progress = "" }
        do {
            guard asset.size > 0 && asset.size <= 150 * 1024 * 1024,
                  asset.browser_download_url.scheme == "https",
                  asset.browser_download_url.host == "github.com",
                  asset.browser_download_url.path.hasPrefix("/\(Self.repository)/releases/download/"),
                  let digest = asset.digest, digest.hasPrefix("sha256:"), digest.count == 71 else {
                throw Failure.reason("发布资产缺少有效的 GitHub SHA-256，或下载来源不正确；拒绝更新。")
            }
            var request = URLRequest(url: asset.browser_download_url)
            request.timeoutInterval = 120
            let (file, response) = try await URLSession.shared.download(for: request)
            defer { try? FileManager.default.removeItem(at: file) }
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw Failure.reason("下载失败。") }
            let bytes = try Data(contentsOf: file, options: .mappedIfSafe)
            guard bytes.count == asset.size, SHA256.hash(data: bytes).map({ String(format: "%02x", $0) }).joined() == String(digest.dropFirst(7)).lowercased() else {
                throw Failure.reason("更新包校验失败，未替换任何程序。")
            }
            let root = FileManager.default.temporaryDirectory.appendingPathComponent("DJOneHub-update-\(UUID().uuidString)", isDirectory: true)
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
            let archive = root.appendingPathComponent("update.zip")
            try bytes.write(to: archive)
            progress = "正在检查并解压…"
            // Archive inspection/extraction must not block AppKit's event loop.
            let tag = release.tag_name
            let payload = try await Task.detached(priority: .utility) { try Self.validateArchive(archive, root: root, version: tag) }.value
            staged = payload; ready = true
            message = "已校验 \(release.tag_name)。点击安装将重启 App 和后台，原配置保留。"
        } catch { message = error.localizedDescription }
    }
    nonisolated static func run(_ tool: String, _ arguments: [String]) throws -> String {
        let process = Process(); process.executableURL = URL(fileURLWithPath: tool); process.arguments = arguments
        let pipe = Pipe(); process.standardOutput = pipe; process.standardError = FileHandle.nullDevice
        try process.run()
        let output = pipe.fileHandleForReading.readDataToEndOfFile(); process.waitUntilExit()
        guard process.terminationStatus == 0 else { throw Failure.reason("更新验证步骤失败：\(URL(fileURLWithPath: tool).lastPathComponent)") }
        return String(decoding: output, as: UTF8.self)
    }
    nonisolated static func validateArchive(_ archive: URL, root: URL, version: String) throws -> URL {
        let entries = try run("/usr/bin/unzip", ["-Z1", archive.path]).split(separator: "\n").map(String.init)
        guard !entries.isEmpty, entries.count < 3000, entries.allSatisfy({ name in
            name.hasPrefix("DJOneHubUpdate/") && !name.contains("\\") && !name.split(separator: "/").contains("..")
        }) else { throw Failure.reason("更新压缩包包含不安全路径。") }
        let listing = try run("/usr/bin/unzip", ["-Z", "-l", archive.path])
        let rows = listing.split(separator: "\n")
        guard !rows.contains(where: { $0.hasPrefix("l") }) else { throw Failure.reason("更新包不允许符号链接。") }
        var total: Int64 = 0
        for row in rows where row.hasPrefix("-") {
            let fields = row.split(whereSeparator: { $0 == " " || $0 == "\t" })
            guard fields.count > 3, let size = Int64(fields[3]), size >= 0, size <= 300 * 1024 * 1024 else { throw Failure.reason("更新包大小异常。") }
            total += size
        }
        guard total > 0 && total <= 600 * 1024 * 1024 else { throw Failure.reason("更新包解压大小超出限制。") }
        _ = try run("/usr/bin/ditto", ["-x", "-k", archive.path, root.path])
        let payload = root.appendingPathComponent("DJOneHubUpdate")
        let app = payload.appendingPathComponent("DJOneHubNotifier.app")
        let plist = try Data(contentsOf: app.appendingPathComponent("Contents/Info.plist"))
        let info = try PropertyListSerialization.propertyList(from: plist, format: nil) as? [String: Any]
        let expected = version.hasPrefix("v") ? String(version.dropFirst()) : version
        guard info?["CFBundleIdentifier"] as? String == "com.jamie.djonehub.notifier", info?["CFBundleShortVersionString"] as? String == expected else { throw Failure.reason("更新包版本或应用标识不匹配。") }
        _ = try run("/usr/bin/codesign", ["--verify", "--deep", "--strict", app.path])
        for path in ["runtime/bin/djonehub-macos", "runtime/lib/libusb-1.0.0.dylib", "DJOneHubNotifier.app/Contents/MacOS/DJOneHubNotifier"] {
            let file = payload.appendingPathComponent(path)
            _ = try run("/usr/bin/lipo", [file.path, "-verify_arch", "arm64", "x86_64"])
            _ = try run("/usr/bin/codesign", ["--verify", "--strict", file.path])
        }
        return payload
    }
    func install() async {
        guard ready, let staged, !busy else { return }
        busy = true
        defer { busy = false }
        do {
            let status = try await api.proxyStatus()
            let calls = try await api.callStatus()
            let setup = try await api.moduleSetupStatus()
            guard status.phase != "reconnecting", calls.active == nil,
                  !["initializing", "restarting", "verifying"].contains(setup.state) else { throw Failure.reason("请等待重连、通话或初始化完成后再更新。") }
            let expected = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/DJOneHub/notifier/DJOneHubNotifier.app").path
            guard Bundle.main.bundleURL.path == expected else { throw Failure.reason("请从已安装的用户目录 App 更新，不能更新磁盘映像或其他副本。") }
            // Backend atomically refuses active write/setup jobs and stops proxy connections.
            try await api.prepareAppUpdate()
            let helper = staged.deletingLastPathComponent().appendingPathComponent("install-update.sh")
            try Self.helper.write(to: helper, atomically: true, encoding: .utf8)
            let log = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/DJOneHub/update.log")
            // launchctl owns the helper independently: booting out the UI must not kill its installer child.
            let process = Process(); process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            process.arguments = ["submit", "-l", "com.jamie.djonehub-update-\(UUID().uuidString)", "-o", log.path, "-e", log.path, "--", "/bin/sh", helper.path, staged.path, "\(ProcessInfo.processInfo.processIdentifier)", release!.tag_name]
            try process.run()
            process.waitUntilExit()
            guard process.terminationStatus == 0 else { throw Failure.reason("无法启动独立更新助手。") }
            NSApp.terminate(nil)
        } catch { message = error.localizedDescription }
    }
    private static let helper = #"""
    #!/bin/sh
    set -eu
    payload=$1; oldpid=$2; tag=$3
    dest="$HOME/Library/Application Support/DJOneHub"
    uid=$(id -u)
    backup="$dest/update-backup-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$backup"
    # Disable KeepAlive before waiting for the current UI to exit.
    launchctl bootout "gui/$uid/com.jamie.djonehub-notifier" >/dev/null 2>&1 || true
    n=0
    while kill -0 "$oldpid" 2>/dev/null; do n=$((n+1)); [ "$n" -lt 20 ] || exit 1; sleep 1; done
    launchctl bootout "gui/$uid/com.jamie.djonehub-wifi-sms-guard" >/dev/null 2>&1 || true
    launchctl bootout "gui/$uid/com.jamie.djonehub" >/dev/null 2>&1 || true
    restore() {
      echo "Update failed; restoring previous files"
      launchctl bootout "gui/$uid/com.jamie.djonehub" >/dev/null 2>&1 || true
      if [ -d "$backup/runtime" ]; then rm -rf "$dest/runtime"; mv "$backup/runtime" "$dest/runtime"; fi
      if [ -d "$backup/DJOneHubNotifier.app" ]; then rm -rf "$dest/notifier/DJOneHubNotifier.app"; mv "$backup/DJOneHubNotifier.app" "$dest/notifier/DJOneHubNotifier.app"; fi
      launchctl bootstrap "gui/$uid" "$HOME/Library/LaunchAgents/com.jamie.djonehub.plist" || true
      launchctl bootstrap "gui/$uid" "$HOME/Library/LaunchAgents/com.jamie.djonehub-notifier.plist" || true
      open "$dest/notifier/DJOneHubNotifier.app" --args --show-window || true
    }
    trap restore EXIT
    /usr/bin/ditto "$payload/runtime" "$dest/runtime.new"
    /usr/bin/ditto "$payload/DJOneHubNotifier.app" "$dest/notifier/DJOneHubNotifier.app.new"
    mv "$dest/runtime" "$backup/runtime"
    mv "$dest/notifier/DJOneHubNotifier.app" "$backup/DJOneHubNotifier.app"
    mv "$dest/runtime.new" "$dest/runtime"
    mv "$dest/notifier/DJOneHubNotifier.app.new" "$dest/notifier/DJOneHubNotifier.app"
    launchctl bootstrap "gui/$uid" "$HOME/Library/LaunchAgents/com.jamie.djonehub.plist"
    ok=0
    for n in $(seq 1 30); do
      if /usr/bin/curl -fsS --max-time 2 http://127.0.0.1:7575/api/platform | /usr/bin/grep -Fq "\"version\":\"${tag#v}\""; then ok=1; break; fi
      sleep 1
    done
    [ "$ok" = 1 ] || exit 1
    launchctl bootstrap "gui/$uid" "$HOME/Library/LaunchAgents/com.jamie.djonehub-notifier.plist"
    if [ -f "$HOME/Library/LaunchAgents/com.jamie.djonehub-wifi-sms-guard.plist" ]; then
      launchctl bootstrap "gui/$uid" "$HOME/Library/LaunchAgents/com.jamie.djonehub-wifi-sms-guard.plist" || true
    fi
    open "$dest/notifier/DJOneHubNotifier.app" --args --show-window
    trap - EXIT
    echo "Update completed; backup retained at $backup"
    """#
}

extension DJOneHubAPI {
    func prepareAppUpdate() async throws {
        struct Empty: Encodable {}
        var request = URLRequest(url: baseURL.appending(path: "api/update/prepare"))
        request.httpMethod = "POST"; request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(Empty()); request.timeoutInterval = 8
        let (_, response) = try await URLSession.shared.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw AppUpdater.Failure.reason("模块正忙，请等待所有写卡、重连、通话操作结束。") }
    }
}

struct SoftwareUpdateView: View {
    @EnvironmentObject var updater: AppUpdater
    @State private var confirm = false
    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            HStack {
                Label("软件更新", systemImage: "arrow.down.app").font(.title2.bold())
                Spacer()
                Text(updater.currentVersion).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary)
            }
            Text("更新源：\(AppUpdater.repository)").font(.callout).foregroundStyle(.secondary)
            Text(updater.message).textSelection(.enabled)
            if !updater.progress.isEmpty { ProgressView(updater.progress) }
            HStack {
                Button("检查更新") { Task { await updater.check() } }.disabled(updater.busy)
                if updater.release != nil && !updater.ready {
                    Button("下载并校验") { Task { await updater.download() } }.disabled(updater.busy)
                }
                if updater.ready {
                    Button("安装并重启") { confirm = true }.buttonStyle(.borderedProminent).disabled(updater.busy)
                }
            }
            if let release = updater.release {
                Divider()
                Text(release.tag_name).font(.headline)
                ScrollView { Text(release.body ?? "此版本没有更新说明。").frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled) }
            }
            Spacer()
            Text("仅安装本仓库发布的通用更新包。校验 GitHub 资产 SHA-256、版本、架构和代码签名；失败时尝试恢复旧程序。当前临时签名不代表 Apple 公证或独立发布者身份认证。")
                .font(.caption).foregroundStyle(.secondary)
        }.padding(28)
        .alert("安装更新并重启？", isPresented: $confirm) {
            Button("取消", role: .cancel) {}
            Button("安装并重启") { Task { await updater.install() } }
        } message: { Text("代理连接将中断，后台和 App 会重启。配置保留，并备份旧程序。请先结束写卡、通话与模块调试。") }
    }
}
