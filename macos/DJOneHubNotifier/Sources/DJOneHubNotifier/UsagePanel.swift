import SwiftUI

struct UsageCycle: Decodable, Hashable {
    var start: String
    var end: String
    var used: Int64
    var rx: Int64
    var tx: Int64
    var proxy: Int64
    var percent: Int
}

struct SIMPlan: Decodable, Hashable {
    var cycleDay: Int
    var quotaMB: Int64
    var autoStop: Bool
    enum CodingKeys: String, CodingKey { case cycleDay = "cycle_day", quotaMB = "quota_mb", autoStop = "auto_stop" }
}

struct UsageCard: Decodable, Identifiable, Hashable {
    var iccid: String
    var name: String
    var imsiPrefix: String
    var firstSeen: String
    var lastSeen: String
    var current: Bool
    var plan: SIMPlan
    var total: Int64
    var cycle: UsageCycle
    var history: [UsageCycle]?
    var id: String { iccid }
    enum CodingKeys: String, CodingKey {
        case iccid, name, current, plan, total, cycle, history
        case imsiPrefix = "imsi_prefix", firstSeen = "first_seen", lastSeen = "last_seen"
    }

    var title: String { name.isEmpty ? UsageCard.carrierLabel(imsiPrefix) : name }
    var shortICCID: String { "···" + iccid.suffix(4) }
    var quotaBytes: Int64 { plan.quotaMB * 1_048_576 }

    /// A readable carrier/region from the IMSI's MCC/MNC.
    static func carrierLabel(_ prefix: String) -> String {
        let mccmnc = String(prefix.prefix(5))
        switch mccmnc {
        case "46000", "46002", "46004", "46007", "46008": return "中国移动"
        case "46001", "46006", "46009": return "中国联通"
        case "46003", "46005", "46011": return "中国电信"
        default: break
        }
        switch String(prefix.prefix(3)) {
        case "454": return "香港卡"
        case "455": return "澳门卡"
        case "466": return "台湾卡"
        case "310", "311", "312", "313", "314", "315", "316": return "美国卡"
        case "502": return "马来西亚卡"
        case "525": return "新加坡卡"
        case "440", "441": return "日本卡"
        case "450": return "韩国卡"
        case "": return "SIM 卡"
        default: return prefix.hasPrefix("2") ? "欧洲卡" : "境外卡"
        }
    }
}

struct UsageNetwork: Decodable {
    var registered: Bool
    var roaming: Bool
    var homePLMN: String
    var servingPLMN: String
    enum CodingKeys: String, CodingKey {
        case registered, roaming
        case homePLMN = "home_plmn", servingPLMN = "serving_plmn"
    }

    /// "中国移动卡 · 漫游接入中国联通网络" style description of the current link.
    var detail: String {
        let home = UsageCard.carrierLabel(homePLMN)
        let card = home.hasSuffix("卡") ? home : home + "卡"
        guard !servingPLMN.isEmpty else { return card }
        let serving = servingPLMN.hasPrefix("460") ? UsageCard.carrierLabel(servingPLMN) : "PLMN \(servingPLMN) "
        return card + (roaming ? " · 漫游接入" : " · ") + serving + "网络"
    }
}

struct UsageStatus: Decodable {
    var currentICCID: String
    var blocked: Bool
    var blockedNote: String
    var error: String
    var cards: [UsageCard]
    var network: UsageNetwork?
    enum CodingKeys: String, CodingKey {
        case blocked, error, cards, network
        case currentICCID = "current_iccid", blockedNote = "blocked_note"
    }
    var currentCard: UsageCard? { cards.first { $0.current } }
}

extension DJOneHubAPI {
    func usageStatus() async throws -> UsageStatus { try await get(path: "api/usage") }
    func usagePlan(iccid: String, name: String, cycleDay: Int, quotaMB: Int64, autoStop: Bool) async throws -> UsageStatus {
        struct Body: Encodable {
            var action = "plan"; var iccid: String; var name: String; var cycleDay: Int; var quotaMB: Int64; var autoStop: Bool
            enum CodingKeys: String, CodingKey { case action, iccid, name; case cycleDay = "cycle_day", quotaMB = "quota_mb", autoStop = "auto_stop" }
        }
        return try await postDecoded(path: "api/usage", body: Body(iccid: iccid, name: name, cycleDay: cycleDay, quotaMB: quotaMB, autoStop: autoStop), timeout: 10)
    }
    func usageCalibrate(iccid: String, usedMB: Double) async throws -> UsageStatus {
        struct Body: Encodable {
            var action = "calibrate"; var iccid: String; var usedMB: Double
            enum CodingKeys: String, CodingKey { case action, iccid; case usedMB = "used_mb" }
        }
        return try await postDecoded(path: "api/usage", body: Body(iccid: iccid, usedMB: usedMB), timeout: 10)
    }
}

/// Polls per-SIM data usage app-wide so quota alerts arrive with the window closed.
@MainActor
final class UsageCenter: ObservableObject {
    @Published var status: UsageStatus?
    @Published var error: String?
    var onAlert: ((String, String) -> Void)?
    private let api: DJOneHubAPI
    init(api: DJOneHubAPI) { self.api = api }

    func refresh() async {
        guard let next = try? await api.usageStatus() else { return }
        status = next
        checkAlerts(next)
    }
    func savePlan(_ card: UsageCard, name: String, cycleDay: Int, quotaMB: Int64, autoStop: Bool) async {
        do { status = try await api.usagePlan(iccid: card.iccid, name: name, cycleDay: cycleDay, quotaMB: quotaMB, autoStop: autoStop); error = nil }
        catch { self.error = error.localizedDescription }
    }
    func calibrate(_ card: UsageCard, usedMB: Double) async {
        do { status = try await api.usageCalibrate(iccid: card.iccid, usedMB: usedMB); error = nil }
        catch { self.error = error.localizedDescription }
    }

    /// One notification per SIM, cycle and threshold.
    private func checkAlerts(_ s: UsageStatus) {
        guard let card = s.currentCard, card.plan.quotaMB > 0 else { return }
        for threshold in [100, 80] where card.cycle.percent >= threshold {
            let key = "djonehub.usageAlert.\(card.iccid).\(card.cycle.start).\(threshold)"
            if UserDefaults.standard.bool(forKey: key) { return }
            UserDefaults.standard.set(true, forKey: key)
            UserDefaults.standard.set(true, forKey: "djonehub.usageAlert.\(card.iccid).\(card.cycle.start).80")
            let used = UsageFormat.bytes(card.cycle.used) + " / " + UsageFormat.bytes(card.quotaBytes)
            let title = threshold >= 100 ? "\(card.title)本期流量已用完" : "\(card.title)本期流量已用 \(card.cycle.percent)%"
            var body = "已用 \(used)，\(UsageFormat.day(card.cycle.end)) 重置。"
            if threshold >= 100 { body += card.plan.autoStop ? "代理已自动暂停。" : "继续使用可能产生超额费用。" }
            onAlert?(title, body)
            return
        }
    }
}

enum UsageFormat {
    static func bytes(_ b: Int64) -> String {
        if b >= 1 << 30 { return String(format: "%.2f GB", Double(b) / 1_073_741_824) }
        if b >= 1 << 20 { return String(format: "%.1f MB", Double(b) / 1_048_576) }
        return "\(b / 1024) KB"
    }
    static func date(_ s: String) -> Date? {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd"
        return f.date(from: s)
    }
    static func day(_ s: String) -> String { date(s)?.formatted(.dateTime.month().day()) ?? s }
    static func daysLeft(_ end: String) -> Int {
        guard let end = date(end) else { return 0 }
        return max(0, Calendar.current.dateComponents([.day], from: Calendar.current.startOfDay(for: Date()), to: end).day ?? 0)
    }
}

/// Compact current-cycle bar for the proxy page.
struct UsageMeter: View {
    var card: UsageCard
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(UsageFormat.bytes(card.cycle.used)).font(.system(.title3, design: .monospaced).weight(.semibold))
                if card.plan.quotaMB > 0 {
                    Text("/ " + UsageFormat.bytes(card.quotaBytes)).font(.caption).foregroundStyle(.secondary)
                }
            }
            if card.plan.quotaMB > 0 {
                ProgressView(value: min(Double(card.cycle.used), Double(card.quotaBytes)), total: Double(max(card.quotaBytes, 1)))
                    .tint(UsageMeter.color(card.cycle.percent))
            }
            Text("\(UsageFormat.day(card.cycle.start)) 起 · 剩 \(UsageFormat.daysLeft(card.cycle.end)) 天重置")
                .font(.caption2).foregroundStyle(.secondary)
        }
    }
    static func color(_ percent: Int) -> Color { percent >= 100 ? .red : (percent >= 80 ? .orange : .accentColor) }
}

struct UsagePanel: View {
    @EnvironmentObject private var center: UsageCenter
    @State private var editing: UsageCard?
    @State private var calibrating: UsageCard?
    @State private var calibrateText = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let status = center.status {
                if status.cards.isEmpty {
                    Text("插卡并联网后自动开始统计。每张卡按 ICCID 单独记账，换卡后再插回来会接着累计。")
                        .font(.system(size: 12)).foregroundStyle(.secondary)
                }
                if status.blocked {
                    Text(status.blockedNote).font(.system(size: 12)).foregroundStyle(.red)
                }
                ForEach(status.cards) { card in
                    cardView(card)
                    if card.id != status.cards.last?.id { Divider() }
                }
                if !status.error.isEmpty { Text(status.error).font(.caption).foregroundStyle(.orange) }
            } else {
                Text("正在读取流量记录…").font(.system(size: 12)).foregroundStyle(.secondary)
            }
            if let error = center.error { Text(error).font(.caption).foregroundStyle(.red) }
            Text("统计模块网卡上的全部蜂窝流量（含 DNS、出口检测、频段测速），按 IP 层计算，已扣除 USB 网卡包头。为估算值，以运营商账单为准；只统计这台模块上的用量。")
                .font(.system(size: 11)).foregroundStyle(.secondary)
        }
        .sheet(item: $editing) { card in
            UsagePlanEditor(card: card) { name, day, quota, autoStop in
                Task { await center.savePlan(card, name: name, cycleDay: day, quotaMB: quota, autoStop: autoStop) }
            }
        }
        .alert("按运营商数据校准", isPresented: Binding(get: { calibrating != nil }, set: { if !$0 { calibrating = nil } })) {
            TextField("本期已用（MB）", text: $calibrateText)
            Button("取消", role: .cancel) {}
            Button("校准") {
                if let card = calibrating, let mb = Double(calibrateText.trimmingCharacters(in: .whitespaces)) {
                    Task { await center.calibrate(card, usedMB: mb) }
                }
            }
        } message: {
            Text("填入运营商 App 或短信显示的本期已用流量（MB，1 GB = 1024 MB）。之后的用量在此基础上继续累计。")
        }
    }

    private func cardView(_ card: UsageCard) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(card.title).font(.system(size: 13, weight: .semibold))
                Text(card.shortICCID).font(.system(size: 11, design: .monospaced)).foregroundStyle(.secondary)
                if card.current { Text("当前").font(.system(size: 10, weight: .semibold)).foregroundStyle(.white).padding(.horizontal, 5).padding(.vertical, 1).background(Color.accentColor, in: Capsule()) }
                Spacer()
                Button("套餐设置") { editing = card }.controlSize(.small)
                Button("校准") { calibrateText = ""; calibrating = card }.controlSize(.small)
            }
            UsageMeter(card: card)
            Text(planLine(card)).font(.system(size: 11)).foregroundStyle(.secondary)
            Text("浏览器代理 \(UsageFormat.bytes(card.cycle.proxy)) · 下行 \(UsageFormat.bytes(card.cycle.rx)) · 上行 \(UsageFormat.bytes(card.cycle.tx)) · 累计 \(UsageFormat.bytes(card.total))")
                .font(.system(size: 11)).foregroundStyle(.secondary)
            if let history = card.history, !history.isEmpty {
                DisclosureGroup("历史周期") {
                    VStack(alignment: .leading, spacing: 4) {
                        ForEach(history, id: \.self) { cycle in
                            HStack {
                                Text("\(UsageFormat.day(cycle.start)) — \(UsageFormat.day(cycle.end))")
                                Spacer()
                                Text(UsageFormat.bytes(cycle.used)).font(.system(size: 11, design: .monospaced))
                                    .foregroundStyle(card.plan.quotaMB > 0 && cycle.used >= card.quotaBytes ? Color.red : Color.secondary)
                            }.font(.system(size: 11))
                        }
                    }.padding(.top, 4)
                }.font(.system(size: 11))
            }
        }
    }

    private func planLine(_ card: UsageCard) -> String {
        var parts = ["每月 \(card.plan.cycleDay) 日重置"]
        parts.append(card.plan.quotaMB > 0 ? "套餐 \(UsageFormat.bytes(card.quotaBytes))" : "未设置套餐流量")
        if card.plan.quotaMB > 0 { parts.append(card.plan.autoStop ? "用完自动暂停代理" : "用完只提醒") }
        if let first = ISO8601DateFormatter().date(from: card.firstSeen) {
            parts.append("自 \(first.formatted(.dateTime.year().month().day())) 起统计")
        }
        return parts.joined(separator: " · ")
    }
}

struct UsagePlanEditor: View {
    var card: UsageCard
    var save: (String, Int, Int64, Bool) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var cycleDay = 1
    @State private var quotaGB = ""
    @State private var autoStop = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("\(card.title) \(card.shortICCID) 套餐设置").font(.headline)
            Form {
                TextField("名称", text: $name, prompt: Text(UsageCard.carrierLabel(card.imsiPrefix)))
                Picker("每月开始日", selection: $cycleDay) {
                    ForEach(1...31, id: \.self) { Text("\($0) 日").tag($0) }
                }
                TextField("每月套餐流量（GB）", text: $quotaGB, prompt: Text("留空或 0 表示不限"))
                Toggle("流量用完时自动暂停代理", isOn: $autoStop).disabled(quotaValue <= 0)
            }
            Text("开始日填激活那天：例如 15 日激活，就是每月 15 日到下月 15 日为一期；没有该日期的月份按当月最后一天算。80% 和用完时会发系统通知。")
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("保存") {
                    save(name.trimmingCharacters(in: .whitespaces), cycleDay, quotaValue, autoStop && quotaValue > 0)
                    dismiss()
                }.buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction)
            }
        }
        .padding(20).frame(width: 420)
        .onAppear {
            name = card.name
            cycleDay = card.plan.cycleDay
            quotaGB = card.plan.quotaMB > 0 ? Self.gbText(card.plan.quotaMB) : ""
            autoStop = card.plan.autoStop
        }
    }
    private var quotaValue: Int64 {
        let gb = Double(quotaGB.trimmingCharacters(in: .whitespaces)) ?? 0
        return gb > 0 ? Int64((gb * 1024).rounded()) : 0
    }
    static func gbText(_ mb: Int64) -> String {
        mb % 1024 == 0 ? "\(mb / 1024)" : String(format: "%.2f", Double(mb) / 1024)
    }
}
