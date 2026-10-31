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
    enum CodingKeys: String, CodingKey {
        case band, fallback
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
    enum CodingKeys: String, CodingKey {
        case running, phase, note, candidates, results, chosen, error, roaming, radio, profile
        case saveData = "save_data", `operator` = "operator"
    }
}

extension DJOneHubAPI {
    func bandStatus() async throws -> BandOptimizerStatus { try await get(path: "api/band") }
    func bandAction(_ action: String, saveData: Bool? = nil) async throws -> BandOptimizerStatus {
        struct Body: Encodable {
            var action: String; var saveData: Bool?
            enum CodingKeys: String, CodingKey { case action; case saveData = "save_data" }
        }
        return try await postDecoded(path: "api/band", body: Body(action: action, saveData: saveData), timeout: 20)
    }
}

struct BandOptimizerPanel: View {
    let api: DJOneHubAPI
    @State private var status: BandOptimizerStatus?
    @State private var saveData = false
    @State private var saveDataTouched = false
    @State private var confirm = false
    @State private var message = ""
    private let timer = Timer.publish(every: 3, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(currentLine).font(.system(size: 13, weight: .medium))
                    Text(qualityLine).font(.system(size: 11)).foregroundStyle(qualityColor)
                }
                Spacer()
                if status?.running == true {
                    ProgressView().controlSize(.small)
                    Button("停止") { Task { await act("stop") } }.controlSize(.small)
                } else {
                    Button("自动选最优频段") { confirm = true }
                        .buttonStyle(.borderedProminent).controlSize(.small)
                        .disabled(status == nil)
                }
            }
            if let profile = status?.profile {
                Text(profileLine(profile)).font(.caption).foregroundStyle(profile.fallback ? Color.orange : Color.secondary)
            }
            Toggle(isOn: Binding(get: { saveData }, set: { saveData = $0; saveDataTouched = true })) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("省流量：只看信号质量，不测速").font(.system(size: 12))
                    Text(status?.roaming == true ? "当前是漫游卡，默认开启（漫游流量贵）" : "国内卡建议关闭，实测速度更准；每个频段约用 10~20 MB")
                        .font(.system(size: 10)).foregroundStyle(.secondary)
                }
            }.toggleStyle(.switch).controlSize(.small).disabled(status?.running == true)
            if let note = status?.note, !note.isEmpty {
                Text(note).font(.callout).foregroundStyle(status?.running == true ? Color.orange : Color.secondary)
            }
            if let results = status?.results, !results.isEmpty {
                VStack(spacing: 4) {
                    ForEach(results) { row($0) }
                }
            }
            if let error = status?.error, !error.isEmpty { Text(error).font(.caption).foregroundStyle(.red) }
            if !message.isEmpty { Text(message).font(.caption).foregroundStyle(.red) }
            HStack {
                Text("按 SIM 卡记住结果，换卡后自动应用；锁定频段后若 30 秒无信号会自动恢复全部频段。测试期间代理、短信和来电会中断，出口 IP 会变化。")
                    .font(.system(size: 10)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 6)
                if status?.profile?.band ?? 0 > 0 || status?.profile?.fallback == true {
                    Button("恢复全部频段") { Task { await act("reset") } }.controlSize(.small).disabled(status?.running == true)
                }
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 11)
        .task { await refresh() }
        .onReceive(timer) { _ in Task { await refresh() } }
        .alert("自动选最优频段？", isPresented: $confirm) {
            Button("取消", role: .cancel) {}
            Button("开始") { Task { await act("optimize", saveData: saveData) } }
        } message: {
            Text("将按当前运营商逐个锁定频段测试（约 2~5 分钟），期间代理、短信和来电会中断，出口 IP 会变化。" + (saveData ? "省流量模式只比较信号质量。" : "每个频段会测速约 5 秒。"))
        }
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
    private func sinrLabel(_ sinr: Double) -> String {
        sinr >= 13 ? "好" : sinr >= 5 ? "一般" : "差"
    }
    private func operatorName(_ plmn: String) -> String {
        switch plmn {
        case "46000", "46002", "46004", "46007", "46008": return "中国移动"
        case "46001", "46006", "46009": return "中国联通"
        case "46003", "46005", "46011": return "中国电信"
        default: return plmn.isEmpty ? "未知运营商" : plmn
        }
    }
    private func profileLine(_ p: BandCardProfile) -> String {
        let when = ISO8601DateFormatter().date(from: p.testedAt)?.formatted(date: .abbreviated, time: .shortened) ?? p.testedAt
        if p.fallback { return "这张卡曾锁定频段，因信号丢失已自动恢复全部频段（测试于 \(when)）" }
        return (p.band > 0 ? "这张卡的最优：Band \(p.band)" : "这张卡的最优：全部频段") + "（\(p.saveData ? "按信号" : "按实测速度")，测试于 \(when)）"
    }
    private func row(_ r: BandResult) -> some View {
        HStack(spacing: 8) {
            Text(r.band == 0 ? "全部频段" : "Band \(r.band)").font(.system(size: 12, weight: r.band == status?.chosen && status?.phase == "done" ? .bold : .regular))
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
            if r.band == status?.chosen && status?.phase == "done" { Image(systemName: "checkmark.circle.fill").foregroundStyle(.green) }
        }
    }
    private func refresh() async {
        guard let next = try? await api.bandStatus() else { return }
        status = next
        if !saveDataTouched && !next.running { saveData = next.roaming }
    }
    private func act(_ action: String, saveData: Bool? = nil) async {
        do { status = try await api.bandAction(action, saveData: saveData); message = "" }
        catch { message = error.localizedDescription }
    }
}
