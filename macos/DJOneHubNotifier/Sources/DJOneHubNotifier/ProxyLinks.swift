import AppKit
import SwiftUI

struct ProxyLink: Decodable, Identifiable {
    var id: String
    var name: String
    var connected: Bool
    var socks: String
    var rotateURL: String
    var publicIP: String
    var lanSocks: String? = nil
    var lanRotateURL: String? = nil
    enum CodingKeys: String, CodingKey {
        case id, name, connected, socks
        case rotateURL = "rotate_url"
        case publicIP = "public_ip"
        case lanSocks = "lan_socks"
        case lanRotateURL = "lan_rotate_url"
    }
}

struct LANShare: Codable, Equatable {
    var enabled: Bool
    var username: String
    var password: String
    var address: String? = nil
}

extension DJOneHubAPI {
    func proxyLinks() async throws -> [ProxyLink] {
        try await get(path: "api/proxy/links")
    }
    func lanShare() async throws -> LANShare { try await get(path: "api/lan-share") }
    func setLANShare(_ value: LANShare) async throws -> LANShare {
        try await postDecoded(path: "api/lan-share", body: LANShare(enabled: value.enabled, username: value.username, password: value.password))
    }
}

/// 蜂窝代理 → 接入信息：每个模块的 SOCKS5 地址和换 IP 链接，复制后直接填进指纹浏览器。
/// 多模块时每个模块一行，端口和链接各不相同。
struct ProxyLinksPanel: View {
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var links: [ProxyLink] = []
    @State private var copied: String?
    @State private var share: LANShare?
    @State private var shareUser = ""
    @State private var sharePass = ""
    @State private var shareBusy = false
    @State private var shareMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("接入信息").font(.headline)
                Spacer()
                Text("填进指纹浏览器的代理设置").font(.caption).foregroundStyle(.secondary)
            }
            if links.isEmpty {
                Text("正在读取…").font(.caption).foregroundStyle(.secondary)
            }
            ForEach(links) { link in
                VStack(alignment: .leading, spacing: 8) {
                    if links.count > 1 {
                        HStack(spacing: 6) {
                            Circle().fill(link.connected ? Color.green : Color.secondary).frame(width: 7, height: 7)
                            Text(link.name.isEmpty ? link.id : link.name).font(.subheadline.weight(.semibold))
                            if !link.publicIP.isEmpty {
                                Text(link.publicIP).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary)
                            }
                        }
                    }
                    row("SOCKS5", link.socks, key: link.id + "socks")
                    row("换 IP 链接", link.rotateURL, key: link.id + "rotate")
                    if let lanSocks = link.lanSocks, let lanRotate = link.lanRotateURL {
                        row("局域网代理", lanSocks, key: link.id + "lansocks")
                        row("局域网换 IP", lanRotate, key: link.id + "lanrotate")
                    }
                }
                .padding(12)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            }
            Text("代理类型选 SOCKS5，主机 127.0.0.1，端口填上面冒号后的数字，不用账号密码。换 IP 链接用 GET 或 POST 都行，不需要请求体；最多 12 秒就回复（指纹浏览器一般只等 15 秒），换得快直接返回新旧 IP，慢的先回“已开始切换”，指纹浏览器会自己检测新 IP；两次至少间隔 8 秒。")
                .font(.caption2).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            Divider()
            lanSharePanel
        }
        .padding(18)
        .background(Color(nsColor: .windowBackgroundColor).opacity(0.6), in: RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.secondary.opacity(0.15)))
        .task {
            // Modules come and go in multi-module mode; keep the list current while visible.
            while !Task.isCancelled {
                if let value = try? await api.proxyLinks() { links = value }
                try? await Task.sleep(for: .seconds(10))
            }
        }
    }

    private var lanSharePanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Toggle("局域网共享", isOn: Binding(
                    get: { share?.enabled ?? false },
                    set: { on in Task { await saveShare(enabled: on) } }
                ))
                .toggleStyle(.switch).controlSize(.small).font(.subheadline.weight(.semibold))
                .disabled(share == nil || shareBusy)
                Spacer()
                if shareBusy { ProgressView().controlSize(.small) }
            }
            HStack(spacing: 8) {
                TextField("用户名", text: $shareUser).textFieldStyle(.roundedBorder).frame(maxWidth: 140)
                TextField("密码", text: $sharePass).textFieldStyle(.roundedBorder).frame(maxWidth: 180)
                Button("保存") { Task { await saveShare(enabled: share?.enabled ?? false) } }
                    .controlSize(.small)
                    .disabled(shareBusy || (shareUser == share?.username && sharePass == share?.password))
            }
            Text("打开后，同一局域网的手机、电脑可以用上面的“局域网代理”地址（SOCKS5，要填这里的用户名和密码）和“局域网换 IP”链接；这台电脑自己仍用 127.0.0.1，不用密码。")
                .font(.caption2).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let shareMessage { Text(shareMessage).font(.caption2).foregroundStyle(.orange) }
        }
        .task {
            if let value = try? await api.lanShare() {
                share = value
                shareUser = value.username
                sharePass = value.password
            }
        }
    }

    private func saveShare(enabled: Bool) async {
        shareBusy = true
        defer { shareBusy = false }
        do {
            let value = try await api.setLANShare(LANShare(
                enabled: enabled,
                username: shareUser.trimmingCharacters(in: .whitespaces),
                password: sharePass.trimmingCharacters(in: .whitespaces)))
            share = value
            shareUser = value.username
            sharePass = value.password
            shareMessage = nil
            if let fresh = try? await api.proxyLinks() { links = fresh }
        } catch {
            shareMessage = error.localizedDescription
        }
    }

    private func row(_ label: String, _ value: String, key: String) -> some View {
        HStack(spacing: 10) {
            Text(label).font(.caption).foregroundStyle(.secondary).frame(width: 72, alignment: .leading)
            Text(value).font(.system(.callout, design: .monospaced)).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
            Spacer(minLength: 8)
            Button(copied == key ? "已复制" : "复制") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(value, forType: .string)
                copied = key
                Task {
                    try? await Task.sleep(for: .seconds(1.5))
                    if copied == key { copied = nil }
                }
            }
            .controlSize(.small)
        }
    }
}
