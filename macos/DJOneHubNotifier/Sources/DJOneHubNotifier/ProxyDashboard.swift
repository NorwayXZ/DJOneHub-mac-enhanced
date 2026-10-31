import SwiftUI
import AppKit

struct ProxyIPVisit: Decodable {
    var ip: String
    var firstSeen: String
    var lastSeen: String
    var location: String
    var isp: String
    enum CodingKeys: String, CodingKey {
        case ip, location, isp
        case firstSeen = "first_seen", lastSeen = "last_seen"
    }
}

struct CellularProxyStatus: Decodable {
    var ipSource: String? = nil
    var history: [ProxyIPVisit]? = nil
    var historyError: String? = nil
    var location: String? = nil
    var isp: String? = nil
    var checkedAt: String? = nil
    var locationError: String? = nil
    var running: Bool
    var address: String
    var interface: String
    var publicIP: String
    var previousIP: String
    var phase: String
    var error: String
    var connections: Int
    var uploaded: UInt64
    var downloaded: UInt64
    enum CodingKeys: String, CodingKey {
        case location, isp, history
        case historyError = "history_error"
        case ipSource = "ip_source"
        case checkedAt = "checked_at", locationError = "location_error"
        case running, address, interface, phase, error, connections, uploaded, downloaded
        case publicIP = "public_ip", previousIP = "previous_ip"
    }
}

struct FleetModule: Decodable, Identifiable {
    var id: String
    var name: String
    var port: Int
    var identity: String
    var connected: Bool
    var needsConfirmation: Bool
    var generation: String
    var interface: String
    var error: String
    var proxy: CellularProxyStatus?
    var address: String { "127.0.0.1:\(port)" }
    enum CodingKeys: String, CodingKey {
        case id, name, port, identity, connected, generation, interface, error, proxy
        case needsConfirmation = "needs_confirmation"
    }
}
struct FleetSnapshot: Decodable { var modules: [FleetModule]; var error: String }
struct ModuleModeStatus: Decodable {
    var enabled: Bool
    var switchAvailable: Bool
    enum CodingKeys: String, CodingKey { case enabled; case switchAvailable = "switch_available" }
}

extension DJOneHubAPI {
    func moduleMode() async throws -> ModuleModeStatus { try await get(path: "api/module-mode") }
    func switchModuleMode(_ enabled: Bool) async throws {
        struct Mode: Encodable { var enabled: Bool; var confirm = true }
        struct Reply: Decodable { var restarting: Bool }
        let _: Reply = try await postDecoded(path: "api/module-mode", body: Mode(enabled: enabled))
    }
    func fleetStatus() async throws -> FleetSnapshot { try await get(path: "api/modules") }
    func confirmModule(_ id: String, generation: String) async throws {
        struct Confirmation: Encodable { var generation: String }
        struct Reply: Decodable { var confirmed: Bool }
        let _: Reply = try await postDecoded(path: "api/modules/\(id)/confirm", body: Confirmation(generation: generation))
    }
    func renameModule(_ id: String, name: String) async throws {
        struct Rename: Encodable { var name: String }
        var request = URLRequest(url: baseURL.appending(path: "api/modules/\(id)"))
        request.httpMethod = "PATCH"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(Rename(name: name))
        let (data, response) = try await URLSession.shared.data(for: request)
        guard let response = response as? HTTPURLResponse else { throw APIError.invalidResponse }
        try Self.requireSuccess(response, data: data)
    }
    func proxyStatus() async throws -> CellularProxyStatus {
        try await get(path: "api/proxy")
    }
    func proxyAction(_ action: String, confirm: Bool = false, moduleID: String? = nil) async throws -> CellularProxyStatus {
        struct Action: Encodable { var action: String; var confirm: Bool }
        return try await postDecoded(path: moduleID.map { "api/modules/\($0)/proxy" } ?? "api/proxy", body: Action(action: action, confirm: confirm), timeout: 25)
    }
}

@MainActor
final class ProxyCenter: ObservableObject {
    @Published var status: CellularProxyStatus? { didSet { onStatusChange?(summaryStatus) } }
    @Published var modules: [FleetModule] = []
    @Published var fleetMode = false
    @Published var selectedID: String? = UserDefaults.standard.string(forKey: "djonehub.selectedModule")
    @Published var error: String?
    @Published private var busyIDs: Set<String> = []
    var previewOnly = false
    var onStatusChange: ((CellularProxyStatus?) -> Void)?
    let api: DJOneHubAPI
    init(api: DJOneHubAPI) { self.api = api }
    var busy: Bool { busyIDs.contains(selectedID ?? "legacy") }
    var selected: FleetModule? { modules.first { $0.id == selectedID } }
    var runningCount: Int { modules.filter { $0.proxy?.running == true }.count }
    var unavailable: Bool { previewOnly || (fleetMode && (selected?.connected != true || selected?.needsConfirmation == true || selected?.proxy == nil)) }
    private var summaryStatus: CellularProxyStatus? {
        guard fleetMode else { return status }
        var summary = status ?? emptyStatus("127.0.0.1:10909")
        summary.running = runningCount > 0
        summary.phase = modules.contains { $0.proxy?.phase == "reconnecting" } ? "reconnecting" : ""
        return summary
    }
    private func emptyStatus(_ address: String) -> CellularProxyStatus {
        CellularProxyStatus(running: false, address: address, interface: "", publicIP: "", previousIP: "", phase: "", error: "", connections: 0, uploaded: 0, downloaded: 0)
    }
    private func applySelection() {
        status = selected?.proxy ?? emptyStatus(selected?.address ?? "127.0.0.1:10909")
    }
    func select(_ id: String) {
        selectedID = id
        if !previewOnly { UserDefaults.standard.set(id, forKey: "djonehub.selectedModule") }
        error = selected?.error.isEmpty == false ? selected?.error : nil
        applySelection()
    }
    func refresh() async {
        guard !previewOnly else { return }
        do {
            guard let mode = try? await api.moduleMode(), mode.enabled else {
                fleetMode = false
                modules = []
                status = try await api.proxyStatus()
                error = nil
                return
            }
            let fleet = try await api.fleetStatus()
            fleetMode = true
            modules = fleet.modules
            if selectedID == nil { selectedID = modules.first?.id }
            error = !fleet.error.isEmpty ? fleet.error : (selected?.error.isEmpty == false ? selected?.error : nil)
            applySelection()
        } catch {
            if fleetMode { self.error = error.localizedDescription; modules = []; status = nil; return }
            do { status = try await api.proxyStatus(); self.error = nil }
            catch { self.error = error.localizedDescription; status = nil }
        }
    }
    func act(_ action: String, confirm: Bool = false) async {
        guard !previewOnly else { return }
        let id = fleetMode ? selectedID : nil
        let key = id ?? "legacy"
        guard !busyIDs.contains(key), !unavailable else { return }
        busyIDs.insert(key)
        defer { busyIDs.remove(key) }
        do {
            let result = try await api.proxyAction(action, confirm: confirm, moduleID: id)
            if id == selectedID || !fleetMode { status = result; error = nil }
            await refresh()
        } catch { if id == selectedID || !fleetMode { self.error = error.localizedDescription } }
    }
    func confirmSelected() async {
        guard !previewOnly, let module = selected else { return }
        do { try await api.confirmModule(module.id, generation: module.generation); await refresh() }
        catch { self.error = error.localizedDescription }
    }
    func renameSelected(_ name: String) async {
        guard !previewOnly, let id = selectedID else { return }
        do { try await api.renameModule(id, name: name); await refresh() }
        catch { self.error = error.localizedDescription }
    }
    func copyAddress() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString("socks5h://" + (selected?.address ?? status?.address ?? "127.0.0.1:10909"), forType: .string)
    }
    var reconnecting: Bool { status?.phase == "reconnecting" }
    var result: String {
        switch status?.phase {
        case "reconnecting": return "正在重新注册蜂窝网络…"
        case "changed": return "已检测到新的公网 IP"
        case "unchanged": return "网络已恢复，运营商分配的 IP 未变"
        case "recovered_unverified": return "网络已恢复，未能可靠比较前后 IP"
        case "failed": return "恢复未完成，请检查模块"
        default: return "尚未执行重连"
        }
    }
}

struct ProxyDashboard: View {
    @EnvironmentObject private var proxy: ProxyCenter
    @State private var confirmReconnect = false
    @State private var confirmBinding = false
    @State private var renameModule = false
    @State private var moduleName = ""
    private let accent = Color(red: 0.05, green: 0.48, blue: 0.39)
    private var disabled: Bool { proxy.busy || proxy.reconnecting || proxy.status == nil || proxy.unavailable }
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("CELLULAR GATEWAY").font(.system(size: 11, weight: .bold, design: .monospaced)).tracking(2).foregroundStyle(accent)
                        Text("蜂窝代理").font(.system(size: 26, weight: .bold, design: .rounded))
                        Text("宽带负责日常连接，SIM 负责指定应用的出口。")
                            .font(.subheadline).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Label(proxy.status?.running == true ? "监听中" : "已停止", systemImage: proxy.status?.running == true ? "circle.fill" : "circle")
                        .font(.caption.weight(.semibold)).foregroundStyle(accent)
                        .padding(10).background(accent.opacity(0.1), in: Capsule())
                }
                if proxy.fleetMode { modulePicker }
                VStack(alignment: .leading, spacing: 14) {
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("蜂窝公网 IPv4").font(.caption).foregroundStyle(.secondary)
                            Text(proxy.status?.publicIP.isEmpty == false ? proxy.status!.publicIP : "尚未检测")
                                .font(.system(size: 26, weight: .medium, design: .monospaced)).textSelection(.enabled)
                            Text(proxy.status?.location?.isEmpty == false ? proxy.status!.location! : "出口归属地：尚未获取")
                                .font(.callout).foregroundStyle(.secondary).textSelection(.enabled)
                            if let isp = proxy.status?.isp, !isp.isEmpty { Text(isp).font(.caption).foregroundStyle(.secondary) }
                            if let source = proxy.status?.ipSource, !source.isEmpty { Text("检测来源：" + source).font(.caption2).foregroundStyle(.secondary) }
                            if let checked = proxy.status?.checkedAt, let date = ISO8601DateFormatter().date(from: checked) {
                                Text("检测时间：" + date.formatted(date: .abbreviated, time: .standard)).font(.caption2).foregroundStyle(.secondary)
                            }
                            if proxy.status?.locationError?.isEmpty == false { Text("归属地查询失败；IP 检测结果仍有效").font(.caption).foregroundStyle(.orange) }
                        }
                        Spacer()
                        Button("检测出口", systemImage: "arrow.up.right.circle") { Task { await proxy.act("probe") } }.disabled(disabled)
                    }
                    Divider()
                    HStack {
                        Label(proxy.selected?.address ?? proxy.status?.address ?? "127.0.0.1:10909", systemImage: "network").font(.system(.body, design: .monospaced))
                        Spacer()
                        Button("复制 SOCKS5 地址", action: proxy.copyAddress)
                    }
                    HStack(spacing: 12) {
                        Button { Task { await proxy.act(proxy.status?.running == true ? "stop" : "start") } } label: {
                            Label(proxy.status?.running == true ? "停止代理" : "启动蜂窝代理", systemImage: proxy.status?.running == true ? "stop.fill" : "play.fill")
                                .frame(minWidth: 130)
                        }.buttonStyle(.borderedProminent).tint(accent).disabled(disabled)
                        Button("重连并尝试换 IP", systemImage: "arrow.triangle.2.circlepath") { confirmReconnect = true }.disabled(disabled)
                        if proxy.busy || proxy.reconnecting { ProgressView().controlSize(.small) }
                    }
                }
                .padding(18)
                .background(LinearGradient(colors: [accent.opacity(0.09), Color(nsColor: .controlBackgroundColor)], startPoint: .topLeading, endPoint: .bottomTrailing), in: RoundedRectangle(cornerRadius: 18))
                HStack(spacing: 12) {
                    metric("模块接口", proxy.status?.interface.isEmpty == false ? proxy.status!.interface : "等待识别")
                    metric("活动连接", "\(proxy.status?.connections ?? 0)")
                    metric("代理流量", ByteCountFormatter.string(fromByteCount: Int64(clamping: (proxy.status?.uploaded ?? 0) &+ (proxy.status?.downloaded ?? 0)), countStyle: .binary))
                }
                VStack(alignment: .leading, spacing: 10) {
                    Label("重连结果", systemImage: "arrow.clockwise.circle").font(.headline)
                    Text(proxy.result).foregroundStyle(.secondary)
                    if let old = proxy.status?.previousIP, !old.isEmpty {
                        Text("上次出口  \(old)").font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    }
                    if let message = proxy.error, !message.isEmpty { Text(message).foregroundStyle(.red).textSelection(.enabled) }
                    if let message = proxy.status?.error, !message.isEmpty { Text(message).foregroundStyle(.orange).textSelection(.enabled) }
                }.padding(18).frame(maxWidth: .infinity, alignment: .leading).background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 14))
                historyView
                DisclosureGroup("连接方式与使用边界") {
                    VStack(alignment: .leading, spacing: 8) {
                    Text("01   在目标应用中配置 SOCKS5，并启用通过代理解析 DNS。")
                    Text("02   仅本机 · IPv4 · TCP。UDP / QUIC 与局域网共享暂不支持。")
                    Text("03   模块断线后代理失败，不改走宽带；公网 IP 不保证独享或每次变化。")
                    Text("04   保持宽带为系统默认网络；本版本不自动修改系统网关。")
                    Text("05   DNS：模块 DHCP 提供；出口检测：myip.ipip.net / api.ipify.org；归属地：ipwho.is（会获知出口 IP）。位置为估算，非 GPS。")
                    }.font(.callout).foregroundStyle(.secondary).padding(.top, 8)
                }
            }.padding(24)
        }
        .alert("重连蜂窝网络？", isPresented: $confirmReconnect) {
            Button("取消", role: .cancel) {}
            Button("断开并重连", role: .destructive) { Task { await proxy.act("reconnect", confirm: true) } }
        } message: {
            Text("所选模块的代理连接将中断，短信与来电会暂时不可用；其他模块继续工作。IP 由运营商分配，不保证更换；两次操作间隔至少 60 秒。")
        }
    }
    private var modulePicker: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("模块与独立出口").font(.headline)
                Spacer()
                Text("\(proxy.runningCount) 个代理运行中").font(.caption).foregroundStyle(.secondary)
            }
            if proxy.modules.isEmpty {
                Text("尚未接入模块。接入后自动识别，每台模块分配独立端口；原有绑定会保留。")
                    .font(.callout).foregroundStyle(.secondary)
            }
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 200), spacing: 10)], spacing: 10) {
                ForEach(proxy.modules) { module in
                    Button { proxy.select(module.id) } label: {
                        VStack(alignment: .leading, spacing: 6) {
                            HStack {
                                Text(module.name).font(.headline)
                                Spacer()
                                Circle().fill(module.proxy?.running == true ? Color.green : Color.gray).frame(width: 7, height: 7)
                            }
                            Text(module.address).font(.system(.caption, design: .monospaced))
                            Text(!module.connected ? "未连接 · 端口保留" : (module.needsConfirmation ? "需确认设备绑定" : (module.proxy?.publicIP.isEmpty == false ? module.proxy!.publicIP : "已连接 · 等待出口检测")))
                                .font(.caption).foregroundStyle(.secondary)
                        }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
                            .background(accent.opacity(proxy.selectedID == module.id ? 0.13 : 0.03), in: RoundedRectangle(cornerRadius: 12))
                            .overlay(RoundedRectangle(cornerRadius: 12).stroke(accent.opacity(proxy.selectedID == module.id ? 0.65 : 0.15)))
                            .contentShape(Rectangle())
                    }.buttonStyle(.plain).modifier(SidebarFocusAppearance())
                }
            }
            if let module = proxy.selected {
                HStack {
                    Text(module.identity).font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    Button("重命名") { moduleName = module.name; renameModule = true }.disabled(proxy.previewOnly)
                    if module.needsConfirmation {
                        Button("确认设备绑定") { confirmBinding = true }.disabled(proxy.previewOnly)
                    }
                }
            }
            Text("每个端口固定绑定一个模块。模块断开时停止对应代理；辅助通话、短信和配置工具目前仅在单模块连接时使用。")
                .font(.caption).foregroundStyle(.secondary)
        }.padding(16).background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
            .alert("确认这个 USB 设备？", isPresented: $confirmBinding) {
                Button("取消", role: .cancel) {}
                Button("确认绑定") { Task { await proxy.confirmSelected() } }
            } message: {
                Text("将当前设备绑定到 \(proxy.selected?.name ?? "模块") 的 \(proxy.selected?.address ?? "")。没有唯一序列号的设备重插后需要再次确认，避免使用错误 SIM。")
            }
            .alert("重命名模块", isPresented: $renameModule) {
                TextField("模块名称", text: $moduleName)
                Button("取消", role: .cancel) {}
                Button("保存") { Task { await proxy.renameSelected(moduleName) } }
            }
    }
    private func historyTime(_ value: String) -> String {
        ISO8601DateFormatter().date(from: value)?.formatted(date: .abbreviated, time: .shortened) ?? value
    }
    private var historyView: some View {
        let visits = proxy.status?.history ?? []
        let currentIP = proxy.status?.publicIP ?? ""
        let previousVisits = visits.dropLast().filter { $0.ip == currentIP }.count
        return VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("IP 使用历史", systemImage: "clock.arrow.circlepath").font(.headline)
                Spacer()
                Text("本机保存 · 最近 500 段记录").font(.caption).foregroundStyle(.secondary)
            }
            if !currentIP.isEmpty && visits.last?.ip == currentIP {
                Text(previousVisits > 0 ? "当前 IP 曾出现过 \(previousVisits) 段记录" : "当前 IP 在保留记录中首次出现")
                    .font(.callout).foregroundStyle(previousVisits > 0 ? Color.orange : Color.secondary)
            }
            if let error = proxy.status?.historyError, !error.isEmpty {
                Text("历史记录读取或保存失败：" + error).font(.caption).foregroundStyle(.orange)
            }
            if visits.isEmpty {
                Text("成功检测蜂窝出口后自动记录；连续检测到相同 IP 会合并。")
                    .font(.callout).foregroundStyle(.secondary)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 12) {
                        ForEach(Array(visits.enumerated().reversed()), id: \.offset) { entry in
                            let visit = entry.element
                            VStack(alignment: .leading, spacing: 4) {
                                HStack {
                                    Text(visit.ip).font(.system(.body, design: .monospaced)).textSelection(.enabled)
                                    if entry.offset == visits.count - 1 { Text("最近").font(.caption).foregroundStyle(accent) }
                                    Spacer()
                                    if visits.prefix(entry.offset).contains(where: { $0.ip == visit.ip }) {
                                        Text("再次出现").font(.caption).foregroundStyle(.orange)
                                    }
                                }
                                Text([visit.location, visit.isp].filter { !$0.isEmpty }.joined(separator: " · "))
                                    .font(.caption).foregroundStyle(.secondary)
                                Text(historyTime(visit.firstSeen) + " — " + historyTime(visit.lastSeen))
                                    .font(.caption2).foregroundStyle(.secondary)
                            }
                            Divider()
                        }
                    }
                }.frame(height: min(CGFloat(visits.count) * 90, 270))
                Text("时间表示成功检测到该 IP 的时间范围；停止代理后记录仍保留。")
                    .font(.caption2).foregroundStyle(.secondary)
            }
        }.padding(18).background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
    }
    private func metric(_ title: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(value).font(.system(.title3, design: .monospaced).weight(.semibold))
        }.frame(maxWidth: .infinity, alignment: .leading).padding(16)
            .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
    }
}
