import AppKit
import SwiftUI

// 蜂窝代理 → 共享给朋友：每个朋友单独的 SOCKS5 账号和流量上限。
// 朋友连模块的共享端口（代理端口 + 10000），用多少记多少，到上限自动停；
// 朋友在流量页（7578 端口 /share）看自己用了多少。

struct ShareAccount: Codable, Identifiable, Equatable {
    var id: String = ""
    var name: String = ""
    var username: String = ""
    var password: String = ""
    var quotaBytes: Int64 = 0
    var resetDay: Int = 0
    var expires: String = ""
    var modules: [String]? = []
    var allowRotate: Bool = false
    var enabled: Bool = true
    var created: String? = nil
    // Read only (from the list).
    var used: Int64? = nil
    var nextReset: String? = nil
    var lastSeen: String? = nil
    var status: String? = nil

    enum CodingKeys: String, CodingKey {
        case id, name, username, password, expires, modules, enabled, created, used, status
        case quotaBytes = "quota_bytes"
        case resetDay = "reset_day"
        case allowRotate = "allow_rotate"
        case nextReset = "next_reset"
        case lastSeen = "last_seen"
    }
}

struct ShareOverview: Decodable {
    var accounts: [ShareAccount]
    var address: String
    var pageURL: String
    var pagePort: Int
    var portOffset: Int
    enum CodingKeys: String, CodingKey {
        case accounts, address
        case pageURL = "page_url"
        case pagePort = "page_port"
        case portOffset = "port_offset"
    }
}

struct ShareSettings: Codable {
    var publicSOCKS: [String: String]
    var publicPage: String
    enum CodingKeys: String, CodingKey {
        case publicSOCKS = "public_socks"
        case publicPage = "public_page"
    }
}

extension DJOneHubAPI {
    func shareAccounts() async throws -> ShareOverview { try await get(path: "api/share/accounts") }
    func saveShareAccount(_ account: ShareAccount) async throws -> ShareOverview {
        try await postDecoded(path: "api/share/accounts", body: account)
    }
    func deleteShareAccount(_ id: String) async throws -> ShareOverview {
        try await postDecoded(path: "api/share/delete", body: ["id": id])
    }
    func resetShareAccount(_ id: String) async throws -> ShareOverview {
        try await postDecoded(path: "api/share/reset", body: ["id": id])
    }
    func shareSettings() async throws -> ShareSettings { try await get(path: "api/share/settings") }
    func saveShareSettings(_ value: ShareSettings) async throws -> ShareSettings {
        try await postDecoded(path: "api/share/settings", body: value)
    }
}

private let gigabyte = 1_073_741_824.0

private func trafficText(_ bytes: Int64) -> String {
    let b = Double(bytes)
    if b >= gigabyte { return String(format: "%.2f GB", b / gigabyte) }
    if b >= 1_048_576 { return String(format: "%.1f MB", b / 1_048_576) }
    return "\(Int(b / 1024)) KB"
}

/// Dates from Go carry fractional seconds, which `.iso8601` does not read.
private func parseDate(_ s: String?) -> Date? {
    guard let s, !s.isEmpty else { return nil }
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let d = f.date(from: s) { return d }
    f.formatOptions = [.withInternetDateTime]
    return f.date(from: s)
}

private struct ShareModuleRef: Identifiable {
    var id: String
    var name: String
    var port: Int
}

struct FriendSharePanel: View {
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var overview: ShareOverview?
    @State private var settings = ShareSettings(publicSOCKS: [:], publicPage: "")
    @State private var modules: [ShareModuleRef] = []
    @State private var editing: ShareAccount?
    @State private var confirmReset: ShareAccount?
    @State private var confirmDelete: ShareAccount?
    @State private var copied: String?
    @State private var message: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("共享给朋友").font(.headline)
                Text("每人单独的账号和流量上限").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button {
                    editing = ShareAccount(password: String(UUID().uuidString.prefix(8)).lowercased(), quotaBytes: Int64(10 * gigabyte))
                } label: { Label("添加朋友", systemImage: "plus") }
                .controlSize(.small)
                .buttonStyle(.borderedProminent)
            }
            let accounts = overview?.accounts ?? []
            if accounts.isEmpty {
                Text("还没有朋友账号。添加后，朋友用自己的用户名和密码连接共享端口，用多少记多少，到上限自动停。")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            ForEach(accounts) { account in
                accountRow(account)
            }
            if !accounts.isEmpty { howToConnect }
            if let message { Text(message).font(.caption2).foregroundStyle(.orange) }
        }
        .padding(18)
        .background(Color(nsColor: .windowBackgroundColor).opacity(0.6), in: RoundedRectangle(cornerRadius: 18))
        .overlay(RoundedRectangle(cornerRadius: 18).stroke(Color.secondary.opacity(0.15)))
        .task {
            if let s = try? await api.shareSettings() { settings = s }
            while !Task.isCancelled {
                await reload()
                try? await Task.sleep(for: .seconds(5))
            }
        }
        .sheet(item: $editing) { account in
            FriendAccountEditor(account: account, modules: modules) { saved in
                let value = try await api.saveShareAccount(saved)
                overview = value
            }
        }
        .alert("把 \(confirmReset?.name ?? "") 的流量清零？", isPresented: Binding(get: { confirmReset != nil }, set: { if !$0 { confirmReset = nil } })) {
            Button("清零") { if let a = confirmReset { Task { await run { try await api.resetShareAccount(a.id) } } } }
            Button("取消", role: .cancel) {}
        } message: { Text("已用流量归零，朋友可以重新用满额度。") }
        .alert("删除 \(confirmDelete?.name ?? "") 的账号？", isPresented: Binding(get: { confirmDelete != nil }, set: { if !$0 { confirmDelete = nil } })) {
            Button("删除", role: .destructive) { if let a = confirmDelete { Task { await run { try await api.deleteShareAccount(a.id) } } } }
            Button("取消", role: .cancel) {}
        } message: { Text("朋友马上不能再用这个账号，用量记录一起删除。") }
    }

    private func accountRow(_ a: ShareAccount) -> some View {
        let used = a.used ?? 0
        let fraction = a.quotaBytes > 0 ? min(1, Double(used) / Double(a.quotaBytes)) : 0
        let ok = (a.status ?? "正常") == "正常"
        let tint: Color = !ok ? .red : fraction >= 0.9 ? .orange : .accentColor
        return VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text(a.name).font(.subheadline.weight(.semibold))
                Text(a.username).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary)
                Text(a.status ?? "正常").font(.caption2)
                    .padding(.horizontal, 7).padding(.vertical, 1)
                    .background(Color.secondary.opacity(0.14), in: Capsule())
                    .foregroundStyle(ok ? Color.primary : Color.red)
                Spacer()
                Toggle("", isOn: Binding(get: { a.enabled }, set: { on in
                    var next = a
                    next.enabled = on
                    Task { await run { try await api.saveShareAccount(next) } }
                }))
                .toggleStyle(.switch).controlSize(.small).labelsHidden()
            }
            if a.quotaBytes > 0 {
                ProgressView(value: fraction).tint(tint)
            }
            HStack(spacing: 10) {
                if a.quotaBytes > 0 {
                    Text("已用 \(trafficText(used)) / \(trafficText(a.quotaBytes))（剩 \(trafficText(max(0, a.quotaBytes - used)))）")
                } else {
                    Text("已用 \(trafficText(used))（不限量）")
                }
                if let reset = parseDate(a.nextReset) {
                    Text("\(Calendar.current.component(.month, from: reset)) 月 \(Calendar.current.component(.day, from: reset)) 日清零")
                }
                if !a.expires.isEmpty { Text("到期 \(a.expires)") }
                if a.allowRotate { Text("可换 IP") }
            }
            .font(.caption).foregroundStyle(.secondary)
            HStack(spacing: 6) {
                Button(copied == a.id ? "已复制" : "复制给朋友") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(friendText(a), forType: .string)
                    copied = a.id
                    Task { try? await Task.sleep(for: .seconds(1.5)); if copied == a.id { copied = nil } }
                }
                Button("编辑") { editing = a }
                Button("流量清零") { confirmReset = a }
                Spacer()
                Button("删除", role: .destructive) { confirmDelete = a }
            }
            .controlSize(.small)
        }
        .padding(12)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 10))
    }

    private var howToConnect: some View {
        let offset = overview?.portOffset ?? 10000
        let mods = modules.isEmpty ? [ShareModuleRef(id: "", name: "代理", port: 10909)] : modules
        return VStack(alignment: .leading, spacing: 6) {
            Text("朋友怎么连").font(.caption.weight(.semibold))
            Text("每个模块有一个共享端口 = 代理端口 + \(offset)（\(mods.map { "\($0.name) \($0.port + offset)" }.joined(separator: "，"))），必须用朋友账号登录，内网穿透过来也一样。局域网里直接用 \(overview?.address ?? "本机 IP"):端口。给外面的朋友：把共享端口和流量页端口 \(overview?.pagePort ?? 7578) 用内网穿透映射出去，再把外网地址填在下面，“复制给朋友”就会用外网地址。不要把 \(mods.map { String($0.port) }.joined(separator: "、")) 本身穿透出去，那个端口本机免密码、不计朋友流量。")
                .font(.caption2).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            ForEach(mods) { m in
                HStack(spacing: 8) {
                    Text("\(m.name) 外网地址").font(.caption).foregroundStyle(.secondary).frame(width: 120, alignment: .leading)
                    TextField("例如 frp.example.com:6001", text: Binding(
                        get: { settings.publicSOCKS[m.id] ?? "" },
                        set: { settings.publicSOCKS[m.id] = $0 }
                    )).textFieldStyle(.roundedBorder)
                }
            }
            HStack(spacing: 8) {
                Text("流量页外网地址").font(.caption).foregroundStyle(.secondary).frame(width: 120, alignment: .leading)
                TextField("例如 http://frp.example.com:6008/share", text: $settings.publicPage).textFieldStyle(.roundedBorder)
                Button("保存") {
                    Task {
                        var clean = settings
                        clean.publicSOCKS = clean.publicSOCKS.filter { !$0.value.trimmingCharacters(in: .whitespaces).isEmpty }
                        if let saved = try? await api.saveShareSettings(clean) { settings = saved }
                    }
                }
                .controlSize(.small)
            }
            Text("流量按代理实际传输的数据计（上传加下载），运营商计费通常多 3%–5%，上限可以留一点余量。")
                .font(.caption2).foregroundStyle(.secondary)
        }
    }

    private func friendText(_ a: ShareAccount) -> String {
        let offset = overview?.portOffset ?? 10000
        let mods = (modules.isEmpty ? [ShareModuleRef(id: "", name: "蜂窝代理", port: 10909)] : modules)
            .filter { (a.modules ?? []).isEmpty || (a.modules ?? []).contains($0.id) }
        var lines = ["\(a.name) 你好，这是我给你开的代理：", ""]
        for m in mods {
            let pub = settings.publicSOCKS[m.id] ?? ""
            let lan = (overview?.address ?? "").isEmpty ? "" : "\(overview!.address):\(m.port + offset)"
            lines.append("【\(m.name)】SOCKS5 \(pub.isEmpty ? (lan.isEmpty ? "（地址见主人说明）" : lan) : pub)")
        }
        lines.append("用户名：\(a.username)")
        lines.append("密码：\(a.password)")
        if a.quotaBytes > 0 {
            lines.append("流量：\(trafficText(a.quotaBytes))" + (a.resetDay > 0 ? "，每月 \(a.resetDay) 日清零" : ""))
        }
        if !a.expires.isEmpty { lines.append("到期：\(a.expires)") }
        let page = settings.publicPage.isEmpty ? (overview?.pageURL ?? "") : settings.publicPage
        if !page.isEmpty {
            let user = a.username.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? a.username
            let pass = a.password.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? a.password
            lines.append("")
            lines.append("查看剩余流量：\(page)?user=\(user)&pass=\(pass)")
        }
        return lines.joined(separator: "\n")
    }

    private func reload() async {
        if let value = try? await api.shareAccounts() { overview = value }
        if let mode = try? await api.moduleMode(), mode.enabled, let fleet = try? await api.fleetStatus() {
            modules = fleet.modules.map { ShareModuleRef(id: $0.id, name: $0.name, port: $0.port) }
        } else {
            modules = []
        }
    }

    private func run(_ action: () async throws -> ShareOverview) async {
        do {
            overview = try await action()
            message = nil
        } catch {
            message = error.localizedDescription
        }
    }
}

private struct FriendAccountEditor: View {
    @Environment(\.dismiss) private var dismiss
    @State var account: ShareAccount
    let modules: [ShareModuleRef]
    let save: (ShareAccount) async throws -> Void
    @State private var quotaGB: Double = 10
    @State private var hasExpiry = false
    @State private var expiry = Date()
    @State private var error: String?
    @State private var busy = false

    init(account: ShareAccount, modules: [ShareModuleRef], save: @escaping (ShareAccount) async throws -> Void) {
        _account = State(initialValue: account)
        self.modules = modules
        self.save = save
        _quotaGB = State(initialValue: (Double(account.quotaBytes) / gigabyte * 100).rounded() / 100)
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        if let d = f.date(from: account.expires) {
            _hasExpiry = State(initialValue: true)
            _expiry = State(initialValue: d)
        } else {
            _expiry = State(initialValue: Calendar.current.date(byAdding: .month, value: 1, to: Date()) ?? Date())
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(account.id.isEmpty ? "添加朋友" : "编辑朋友账号").font(.headline)
            Form {
                TextField("称呼", text: $account.name, prompt: Text("例如 小王"))
                TextField("用户名", text: $account.username, prompt: Text("字母或数字"))
                TextField("密码", text: $account.password)
                HStack {
                    TextField("流量上限（GB）", value: $quotaGB, format: .number)
                    Text("0 = 不限").font(.caption).foregroundStyle(.secondary)
                }
                Picker("每月清零", selection: $account.resetDay) {
                    Text("不自动清零").tag(0)
                    ForEach(1...28, id: \.self) { Text("每月 \($0) 日").tag($0) }
                }
                Toggle("设到期日", isOn: $hasExpiry)
                if hasExpiry {
                    DatePicker("到期日", selection: $expiry, displayedComponents: .date)
                }
                if modules.count > 1 {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("可用模块（都不勾 = 全部）").font(.caption).foregroundStyle(.secondary)
                        ForEach(modules) { m in
                            Toggle(m.name, isOn: Binding(
                                get: { (account.modules ?? []).contains(m.id) },
                                set: { on in
                                    var list = account.modules ?? []
                                    list.removeAll { $0 == m.id }
                                    if on { list.append(m.id) }
                                    account.modules = list
                                }
                            ))
                        }
                    }
                }
                Toggle("允许朋友自己换 IP（会同时换掉你在这个模块上的 IP）", isOn: $account.allowRotate)
            }
            if let error { Text(error).font(.caption).foregroundStyle(.red) }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("保存") {
                    busy = true
                    var value = account
                    value.quotaBytes = Int64(max(0, quotaGB) * gigabyte)
                    if hasExpiry {
                        let f = DateFormatter()
                        f.dateFormat = "yyyy-MM-dd"
                        value.expires = f.string(from: expiry)
                    } else {
                        value.expires = ""
                    }
                    value.used = nil; value.nextReset = nil; value.lastSeen = nil; value.status = nil
                    Task {
                        do {
                            try await save(value)
                            dismiss()
                        } catch {
                            self.error = error.localizedDescription
                        }
                        busy = false
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(busy || account.username.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 440)
    }
}
