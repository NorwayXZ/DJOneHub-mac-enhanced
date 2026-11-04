import SwiftUI

struct KeepAlivePlan: Codable {
    var enabled: Bool
    var everyDays: Int?
    var to: String?
    var text: String?
    var phone: String?
    var lastDone: String?
    var lastError: String?
    var lastSeen: String?
    enum CodingKeys: String, CodingKey {
        case enabled, to, text, phone
        case everyDays = "every_days", lastDone = "last_done", lastError = "last_error", lastSeen = "last_seen"
    }
}

struct KeepAliveCard: Codable, Identifiable {
    var iccid: String
    var current: Bool
    var plan: KeepAlivePlan
    var nextDue: String?
    var id: String { iccid }
    enum CodingKeys: String, CodingKey { case iccid, current, plan; case nextDue = "next_due" }
}

struct KeepAliveStatus: Codable {
    var currentICCID: String
    var ownNumber: String
    var cards: [KeepAliveCard]
    enum CodingKeys: String, CodingKey { case cards; case currentICCID = "current_iccid", ownNumber = "own_number" }
}

extension DJOneHubAPI {
    func keepAliveStatus() async throws -> KeepAliveStatus { try await get(path: "api/sim/keepalive", timeout: 15) }
    func saveKeepAlive(iccid: String, enabled: Bool, days: Int, to: String, text: String) async throws -> KeepAliveStatus {
        struct Body: Encodable { var iccid: String; var enabled: Bool; var every_days: Int; var to: String; var text: String }
        return try await postDecoded(path: "api/sim/keepalive", body: Body(iccid: iccid, enabled: enabled, every_days: days, to: to, text: text), timeout: 15)
    }
    func keepAliveAction(_ action: String, iccid: String = "") async throws -> KeepAliveStatus {
        struct Body: Encodable { var action: String; var iccid: String }
        return try await postDecoded(path: "api/sim/keepalive", body: Body(action: action, iccid: iccid), timeout: 60)
    }
}

/// 设置 → SIM 保号：每隔 N 天用这张卡发一条短信，防止长期不用被运营商回收。
struct SIMKeepAlivePanel: View {
    @EnvironmentObject private var proxy: ProxyCenter
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var status: KeepAliveStatus?
    @State private var enabled = false
    @State private var days = 90
    @State private var to = ""
    @State private var text = "保号"
    @State private var busy = false
    @State private var message: (String, Bool)?

    private var current: KeepAliveCard? { status?.cards.first { $0.current } }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("很多国外卡几个月不用就会被回收。开启后，插着这张卡的模块每隔一段时间自动发一条短信；手动发短信也算一次。到期时卡不在任何模块上，会通过 Telegram / 微信提醒你插卡。")
                .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let card = current {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Label(currentTitle(card), systemImage: "simcard")
                            .font(.system(size: 12, weight: .medium))
                        Spacer()
                        Toggle("开启保号", isOn: $enabled).toggleStyle(.switch).controlSize(.small)
                    }
                    HStack(spacing: 8) {
                        Picker("每隔", selection: $days) {
                            ForEach([30, 60, 90, 120, 150, 180], id: \.self) { Text("\($0) 天").tag($0) }
                        }.frame(maxWidth: 140)
                        TextField("发给（留空 = 发给本机号码）", text: $to).textFieldStyle(.roundedBorder).frame(maxWidth: 210)
                        TextField("内容", text: $text).textFieldStyle(.roundedBorder).frame(maxWidth: 120)
                    }.font(.system(size: 12))
                    HStack(spacing: 8) {
                        Button("保存") { Task { await save(card.iccid) } }.buttonStyle(.borderedProminent).controlSize(.small).disabled(busy)
                        Button("立即发一次") { Task { await run() } }.controlSize(.small).disabled(busy)
                        if busy { ProgressView().controlSize(.small) }
                        Spacer()
                        Text(lastLine(card)).font(.system(size: 10)).foregroundStyle(card.plan.lastError?.isEmpty == false ? Color.orange : Color.secondary)
                    }
                }
                .padding(10)
                .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
            } else if status != nil {
                Text("读不到 SIM 卡。").font(.caption).foregroundStyle(.orange)
            } else {
                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("正在读取…").font(.caption).foregroundStyle(.secondary) }
            }
            let others = (status?.cards ?? []).filter { !$0.current }
            if !others.isEmpty {
                Text("其他卡").font(.system(size: 12, weight: .medium))
                ForEach(others) { card in
                    HStack(spacing: 8) {
                        Image(systemName: card.plan.enabled ? "checkmark.circle.fill" : "circle").foregroundStyle(card.plan.enabled ? Color.green : Color.secondary)
                        Text(short(card.iccid) + (card.plan.phone.map { " · " + $0 } ?? "")).font(.system(size: 11, design: .monospaced))
                        Spacer()
                        Text(lastLine(card)).font(.system(size: 10)).foregroundStyle(overdue(card) ? Color.orange : Color.secondary)
                        Button("移除") { Task { await remove(card.iccid) } }.controlSize(.mini).disabled(busy)
                    }
                }
                Text("其他卡的保号由插着它的模块执行；插到任一模块上即可。").font(.system(size: 10)).foregroundStyle(.secondary)
            }
            if let (msg, ok) = message { Text(msg).font(.system(size: 11)).foregroundStyle(ok ? Color.green : Color.orange) }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .task { await load() }
    }

    private func currentTitle(_ card: KeepAliveCard) -> String {
        var title = "当前卡 " + short(card.iccid)
        if let number = status?.ownNumber, !number.isEmpty { title += " · " + number }
        if proxy.fleetMode, let name = proxy.selected?.name { title += " · " + name }
        return title
    }
    private func short(_ iccid: String) -> String { iccid.count > 6 ? "…" + iccid.suffix(6) : iccid }
    private func date(_ s: String?) -> Date? { s.flatMap { ISO8601DateFormatter().date(from: $0) } }
    private func overdue(_ card: KeepAliveCard) -> Bool { card.plan.enabled && (date(card.nextDue).map { $0 < Date() } ?? false) }
    private func lastLine(_ card: KeepAliveCard) -> String {
        if let error = card.plan.lastError, !error.isEmpty { return error }
        guard let last = date(card.plan.lastDone) else { return card.plan.enabled ? "还没发过，开启后几分钟内会发第一次" : "未开启" }
        var line = "上次 " + last.formatted(date: .abbreviated, time: .omitted)
        if card.plan.enabled, let next = date(card.nextDue) { line += " · 下次 " + next.formatted(date: .abbreviated, time: .omitted) + (next < Date() ? "（已到期）" : "") }
        return line
    }
    private func apply(_ next: KeepAliveStatus) {
        status = next
        if let plan = current?.plan {
            enabled = plan.enabled
            days = (plan.everyDays ?? 0) > 0 ? plan.everyDays! : 90
            to = plan.to ?? ""
            text = (plan.text?.isEmpty == false) ? plan.text! : "保号"
        }
    }
    private func load() async {
        do { apply(try await api.keepAliveStatus()); message = nil }
        catch { message = (error.localizedDescription, false) }
    }
    private func save(_ iccid: String) async {
        busy = true
        defer { busy = false }
        do { apply(try await api.saveKeepAlive(iccid: iccid, enabled: enabled, days: days, to: to, text: text)); message = ("已保存", true) }
        catch { message = (error.localizedDescription, false) }
    }
    private func run() async {
        busy = true
        defer { busy = false }
        do { apply(try await api.keepAliveAction("run")); message = ("保号短信已发出", true) }
        catch { message = (error.localizedDescription, false) }
    }
    private func remove(_ iccid: String) async {
        busy = true
        defer { busy = false }
        do { apply(try await api.keepAliveAction("delete", iccid: iccid)); message = nil }
        catch { message = (error.localizedDescription, false) }
    }
}
