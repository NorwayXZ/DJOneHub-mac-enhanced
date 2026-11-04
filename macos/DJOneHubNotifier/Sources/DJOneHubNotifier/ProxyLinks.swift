import AppKit
import SwiftUI

struct ProxyLink: Decodable, Identifiable {
    var id: String
    var name: String
    var connected: Bool
    var socks: String
    var rotateURL: String
    var publicIP: String
    enum CodingKeys: String, CodingKey {
        case id, name, connected, socks
        case rotateURL = "rotate_url"
        case publicIP = "public_ip"
    }
}

extension DJOneHubAPI {
    func proxyLinks() async throws -> [ProxyLink] {
        try await get(path: "api/proxy/links")
    }
}

/// 蜂窝代理 → 接入信息：每个模块的 SOCKS5 地址和换 IP 链接，复制后直接填进指纹浏览器。
/// 多模块时每个模块一行，端口和链接各不相同。
struct ProxyLinksPanel: View {
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var links: [ProxyLink] = []
    @State private var copied: String?

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
                }
                .padding(12)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
            }
            Text("代理类型选 SOCKS5，主机 127.0.0.1，端口填上面冒号后的数字，不用账号密码。换 IP 链接用 GET 或 POST 都行，不需要请求体；最多 12 秒就回复（指纹浏览器一般只等 15 秒），换得快直接返回新旧 IP，慢的先回“已开始切换”，指纹浏览器会自己检测新 IP；两次至少间隔 8 秒。链接只在这台 Mac 上能用。")
                .font(.caption2).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
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

    private func row(_ label: String, _ value: String, key: String) -> some View {
        HStack(spacing: 10) {
            Text(label).font(.caption).foregroundStyle(.secondary).frame(width: 64, alignment: .leading)
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
