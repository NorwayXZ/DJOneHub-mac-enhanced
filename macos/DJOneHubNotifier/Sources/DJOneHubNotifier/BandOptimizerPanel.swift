import SwiftUI

// MARK: - 频段优化：逐个锁定运营商在大陆使用的频段实测，按 SIM 记住最优结果

struct BandRadio: Decodable {
    var band: Int
    var plmn: String
    var rsrp: Int
    var sinr: Double
    var rsrq: Int
    var valid: Bool
}

struct BandResult: Decodable, Identifiable {
    var band: Int
    var status: String
    var servingBand: Int
    var rsrp: Int
    var sinr: Double
    var rsrq: Int
    var speedMBps: Double
    var detail: String
    var id: Int { band }
    enum CodingKeys: String, CodingKey {
        case band, status, rsrp, sinr, rsrq, detail
        case servingBand = "serving_band", speedMBps = "speed_mbps"
    }
}

struct BandCardProfile: Decodable {
    var band: Int
    var testedAt: String
    var saveData: Bool
    var fallback: Bool
    var disabled: Bool?
    var manual: Bool?
    var best: Int?
    var results: [BandResult]?
    /// The band the test chose (0 = all bands), even after a manual pick.
    var recommended: Int { manual == true ? (best ?? 0) : band }
    enum CodingKeys: String, CodingKey {
        case band, fallback, disabled, manual, best, results
        case testedAt = "tested_at", saveData = "save_data"
    }
}

struct BandOptimizerStatus: Decodable {
    var running: Bool
    var phase: String
    var note: String
    var saveData: Bool
    var `operator`: String
    var candidates: [Int]?
    var results: [BandResult]?
    var chosen: Int
    var error: String
    var roaming: Bool
    var radio: BandRadio
    var profile: BandCardProfile?
    var lockedBand: Int?
    var maskKnown: Bool?
    enum CodingKeys: String, CodingKey {
        case running, phase, note, candidates, results, chosen, error, roaming, radio, profile
        case saveData = "save_data", `operator` = "operator"
        case lockedBand = "locked_band", maskKnown = "mask_known"
    }
}

extension DJOneHubAPI {
    func bandStatus() async throws -> BandOptimizerStatus { try await get(path: "api/band") }
    func bandAction(_ action: String, saveData: Bool? = nil, band: Int? = nil) async throws -> BandOptimizerStatus {
        struct Body: Encodable {
            var action: String; var saveData: Bool?; var band: Int?
            enum CodingKeys: String, CodingKey { case action, band; case saveData = "save_data" }
        }
        return try await postDecoded(path: "api/band", body: Body(action: action, saveData: saveData, band: band), timeout: 20)
    }
}

/// Polls the optimizer app-wide so a finished test is announced even when the
/// settings page is closed (a run takes 2–5 minutes).
@MainActor
final class BandCenter: ObservableObject {
    @Published var status: BandOptimizerStatus?
    @Published var error: String?
    @Published var busy = false
    var onFinish: ((String, String) -> Void)?
    private let api: DJOneHubAPI
    init(api: DJOneHubAPI) { self.api = api }

    func refresh() async {
        guard let next = try? await api.bandStatus() else { return }
        let wasRunning = status?.running == true
        status = next
        if wasRunning && !next.running { onFinish?(Self.finishTitle(next), next.note) }
    }
    func act(_ action: String, saveData: Bool? = nil, band: Int? = nil) async {
        busy = true
        defer { busy = false }
        do { status = try await api.bandAction(action, saveData: saveData, band: band); error = nil }
        catch { self.error = error.localizedDescription }
    }
    static func finishTitle(_ s: BandOptimizerStatus) -> String {
        switch s.phase {
        case "done": return s.chosen > 0 ? "频段测试完成：已锁定 Band \(s.chosen)" : "频段测试完成：保持全部频段"
        case "stopped": return "频段测试已停止"
        default: return "频段测试未完成"
        }
    }
}

struct BandOptimizerPanel: View {
    @EnvironmentObject private var center: BandCenter
    @State private var saveData = false
    @State private var confirm = false
    @State private var dismissedBanner = ""
    @State private var showResults = false
    private var status: BandOptimizerStatus? { center.status }
    private var profile: BandCardProfile? { status?.profile }
    private var running: Bool { status?.running == true }
    private var lockedBand: Int { status?.lockedBand ?? 0 }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(currentLine).font(.system(size: 13, weight: .medium))
                    Text(qualityLine).font(.system(size: 11)).foregroundStyle(qualityColor)
                }
                Spacer()
                if running {
                    ProgressView().controlSize(.small)
                    Button("停止测试") { Task { await center.act("stop") } }.controlSize(.small)
                } else {
                    Button(profile == nil ? "自动选最优频段" : "重新测试") { confirm = true }
                        .buttonStyle(.borderedProminent).controlSize(.small)
                        .disabled(status == nil || center.busy)
                }
            }

            if let banner, banner.key != dismissedBanner {
                HStack(alignment: .top, spacing: 8) {
                    Image(systemName: banner.ok ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                        .foregroundStyle(banner.ok ? Color.green : Color.orange)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(banner.title).font(.system(size: 12, weight: .semibold))
                        Text(banner.detail).font(.system(size: 11)).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("知道了") { dismissedBanner = banner.key }.controlSize(.small).buttonStyle(.borderless)
                }
                .padding(10)
                .background((banner.ok ? Color.green : Color.orange).opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
            }

            // The switch shows what the module is actually doing, read back every 10s.
            HStack(alignment: .center, spacing: 10) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(status?.maskKnown != true ? "正在读取模块频段设置…" : lockedBand > 0 ? "已锁定 Band \(lockedBand)" : "使用全部频段（模块自动选择）")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(lockedBand > 0 ? Color.green : Color.primary)
                    Text(modeDetail).font(.system(size: 11)).foregroundStyle(profile?.fallback == true ? Color.orange : Color.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer()
                if center.busy && !running { ProgressView().controlSize(.small) }
                Toggle("锁定频段", isOn: Binding(
                    get: { lockedBand > 0 },
                    set: { on in Task { await center.act(on ? "lock" : "unlock") } }
                ))
                .labelsHidden().toggleStyle(.switch)
                .disabled(running || center.busy || status?.maskKnown != true || (lockedBand == 0 && (profile?.band ?? 0) == 0))
                .help(lockedBand > 0 ? "关闭后恢复全部频段，保留测试结果，随时可再打开" : (profile?.manual == true ? "打开后锁定你上次选的 Band \(profile?.band ?? 0)" : "打开后锁定这张卡测得的最优频段"))
            }
            .padding(10)
            .background(Color.primary.opacity(0.04), in: RoundedRectangle(cornerRadius: 8))

            if running, let note = status?.note, !note.isEmpty {
                Text(note).font(.callout).foregroundStyle(.orange)
            }
            if !shownResults.isEmpty {
                DisclosureGroup(isExpanded: Binding(get: { running || showResults }, set: { showResults = $0 })) {
                    VStack(spacing: 4) {
                        ForEach(shownResults) { row($0) }
                        if !running {
                            Text("“推荐”是测试按速度选的；觉得实际体验不好，可以点任意一行的“锁定”改用那个频段，或点“全部频段”那行恢复自动选择。切换时代理会断开几秒，出口 IP 可能变化。")
                                .font(.system(size: 10)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                                .frame(maxWidth: .infinity, alignment: .leading).padding(.top, 2)
                        }
                    }.padding(.top, 4)
                } label: {
                    Text(running ? "测试进度" : "上次测试结果" + (testedWhen.isEmpty ? "" : "（\(testedWhen)）"))
                        .font(.system(size: 12, weight: .medium))
                }
            }

            Toggle(isOn: $saveData) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("省流量测试：只看信号质量，不测速").font(.system(size: 12))
                    Text(status?.roaming == true ? "默认关闭：按实测速度选频段；漫游卡每个频段约用 10~20 MB 漫游流量" : "默认关闭：按实测速度选频段，每个频段约用 10~20 MB")
                        .font(.system(size: 10)).foregroundStyle(.secondary)
                }
            }.toggleStyle(.switch).controlSize(.small).disabled(running)
            if let error = status?.error, !error.isEmpty { Text(error).font(.caption).foregroundStyle(.red) }
            if let error = center.error { Text(error).font(.caption).foregroundStyle(.red) }
            Text("结果按 SIM 卡保存，换卡后自动应用；锁定后若 30 秒无信号会自动切回全部频段。测试期间代理、短信和来电会中断，出口 IP 会变化。")
                .font(.system(size: 10)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, 14).padding(.vertical, 11)
        .task { await center.refresh() }
        .alert(profile == nil ? "自动选最优频段？" : "重新测试频段？", isPresented: $confirm) {
            Button("取消", role: .cancel) {}
            Button("开始") { Task { await center.act("optimize", saveData: saveData) } }
        } message: {
            Text("将按当前运营商逐个锁定频段测试（约 2~5 分钟），期间代理、短信和来电会中断，出口 IP 会变化。" + (saveData ? "省流量模式只比较信号质量。" : "每个频段会测速约 5 秒。") + "完成后会通知你。")
        }
    }

    // MARK: derived text

    private var banner: (key: String, ok: Bool, title: String, detail: String)? {
        guard let s = status, !s.running, ["done", "stopped", "failed"].contains(s.phase) else { return nil }
        let key = s.phase + (profile?.testedAt ?? s.note)
        var detail = s.note
        if s.phase == "done", s.chosen > 0, let gain = speedGain(s.results ?? [], band: s.chosen) { detail = gain + "。" + s.note }
        return (key, s.phase == "done", BandCenter.finishTitle(s), detail)
    }
    private var modeDetail: String {
        guard let p = profile else { return "还没测试这张卡。点“自动选最优频段”找出最快的频段。" }
        if p.fallback { return "Band \(p.band) 曾因信号丢失，已自动切回全部频段；打开开关可重新锁定。" }
        if lockedBand > 0 && p.manual == true {
            let measured = speedGain(p.results ?? [], band: lockedBand).map { "（\($0)）" } ?? ""
            return "你手动锁定了这个频段" + measured + "；测试推荐的是" + (p.recommended > 0 ? "Band \(p.recommended)" : "全部频段") + "。关闭开关即恢复全部频段。"
        }
        if lockedBand > 0 {
            return (speedGain(p.results ?? [], band: lockedBand) ?? "这张卡测得的最优频段") + "。关闭开关即恢复全部频段，测试结果会保留。"
        }
        if p.band > 0 { return "这张卡测得最优是 Band \(p.band)" + (speedGain(p.results ?? [], band: p.band).map { "（\($0)）" } ?? "") + "，打开开关即可锁定。" }
        return "这张卡测试后，锁定单个频段没有明显更快，保持全部频段最稳。"
    }
    private func speedGain(_ results: [BandResult], band: Int) -> String? {
        guard let best = results.first(where: { $0.band == band && $0.status == "ok" }) else { return nil }
        if let all = results.first(where: { $0.band == 0 && $0.status == "ok" }), all.speedMBps > 0, best.speedMBps > 0 {
            return String(format: "实测 %.2f MB/s，约为全部频段（%.2f MB/s）的 %.1f 倍", best.speedMBps, all.speedMBps, best.speedMBps / all.speedMBps)
        }
        return String(format: "信噪比 %.1f dB", best.sinr)
    }
    private var shownResults: [BandResult] {
        if let live = status?.results, !live.isEmpty { return live }
        return profile?.results ?? []
    }
    private var testedWhen: String {
        guard let t = profile?.testedAt, let d = ISO8601DateFormatter().date(from: t) else { return "" }
        return d.formatted(date: .abbreviated, time: .shortened)
    }
    private var currentLine: String {
        guard let radio = status?.radio, radio.valid else { return "当前频段：读取中…" }
        return "当前 Band \(radio.band) · \(operatorName(radio.plmn))"
    }
    private var qualityLine: String {
        guard let radio = status?.radio, radio.valid else { return "" }
        return String(format: "信号 %d dBm · 信噪比 %.1f dB（%@）· 质量 %d dB", radio.rsrp, radio.sinr, sinrLabel(radio.sinr), radio.rsrq)
    }
    private var qualityColor: Color {
        guard let sinr = status?.radio.sinr, status?.radio.valid == true else { return .secondary }
        return sinr < 3 ? .orange : .secondary
    }
    private func sinrLabel(_ sinr: Double) -> String { sinr >= 13 ? "好" : sinr >= 5 ? "一般" : "差" }
    private func operatorName(_ plmn: String) -> String {
        switch plmn {
        case "46000", "46002", "46004", "46007", "46008": return "中国移动"
        case "46001", "46006", "46009": return "中国联通"
        case "46003", "46005", "46011": return "中国电信"
        default: return plmn.isEmpty ? "未知运营商" : plmn
        }
    }
    private func row(_ r: BandResult) -> some View {
        let picked = r.band == (running ? -1 : (status?.phase == "done" && profile == nil ? status?.chosen : profile?.recommended))
        let inUse = status?.maskKnown == true && (r.band == 0 ? lockedBand == 0 : lockedBand == r.band)
        return HStack(spacing: 8) {
            Text(r.band == 0 ? "全部频段" : "Band \(r.band)").font(.system(size: 12, weight: picked ? .bold : .regular))
                .frame(width: 72, alignment: .leading)
            switch r.status {
            case "testing":
                ProgressView().controlSize(.mini)
                Text("测试中…").font(.caption).foregroundStyle(.secondary)
            case "ok":
                Text(String(format: "%d dBm · %.1f dB", r.rsrp, r.sinr)).font(.system(size: 11, design: .monospaced)).foregroundStyle(.secondary)
                if r.band == 0 && r.servingBand > 0 { Text("(B\(r.servingBand))").font(.caption2).foregroundStyle(.secondary) }
                Spacer()
                if r.speedMBps > 0 { Text(String(format: "%.2f MB/s", r.speedMBps)).font(.system(size: 11, weight: .medium, design: .monospaced)) }
            default:
                Text(r.detail.isEmpty ? "失败" : r.detail).font(.caption).foregroundStyle(.orange)
            }
            Spacer(minLength: 0)
            if picked {
                Text("推荐").font(.system(size: 10, weight: .semibold)).foregroundStyle(.green)
                    .padding(.horizontal, 5).padding(.vertical, 1).background(Color.green.opacity(0.12), in: Capsule())
            }
            if !running {
                if inUse {
                    Label("使用中", systemImage: "checkmark.circle.fill").font(.system(size: 11, weight: .medium)).foregroundStyle(.green)
                        .frame(width: 70, alignment: .trailing)
                } else if r.band == 0 || r.status == "ok" {
                    Button(r.band == 0 ? "改用" : "锁定") {
                        Task { await center.act(r.band == 0 ? "unlock" : "lock", band: r.band == 0 ? nil : r.band) }
                    }
                    .controlSize(.small).disabled(center.busy || status?.maskKnown != true)
                    .frame(width: 70, alignment: .trailing)
                } else {
                    Color.clear.frame(width: 70, height: 1)
                }
            }
        }
    }
}
