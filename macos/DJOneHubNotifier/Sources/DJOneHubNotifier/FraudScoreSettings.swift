import SwiftUI

struct IPQSSettings: Decodable {
    var configured: Bool
    var keyHint: String? = nil
    var credits: Int? = nil
    var usage: Int? = nil
    var error: String? = nil
    enum CodingKeys: String, CodingKey {
        case configured, credits, usage, error
        case keyHint = "key_hint"
    }
}

extension DJOneHubAPI {
    func ipqsSettings() async throws -> IPQSSettings {
        try await get(path: "api/fraud/ipqs")
    }
    func setIPQSKey(_ key: String) async throws -> IPQSSettings {
        struct Body: Encodable { var api_key: String }
        return try await postDecoded(path: "api/fraud/ipqs", body: Body(api_key: key), timeout: 25)
    }
}

/// 设置 → IP 欺诈值：Scamalytics 始终启用；填入 IPQualityScore API Key 后自动加查 IPQS。
struct FraudScoreSettingsPanel: View {
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var settings: IPQSSettings?
    @State private var draft = ""
    @State private var editing = false
    @State private var busy = false
    @State private var message: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("每个出口 IP 都会经宽带查询 Scamalytics 欺诈值，不花蜂窝流量。填入 IPQualityScore 的 API Key 后会自动同时查询 IPQS，留空则不查。")
                .font(.system(size: 11)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack(spacing: 8) {
                Text("IPQS API Key").font(.system(size: 13, weight: .medium))
                Spacer(minLength: 4)
                if settings?.configured == true && !editing {
                    Text(settings?.keyHint ?? "已填写").font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary)
                    Button("更换") { draft = ""; editing = true }.controlSize(.small)
                    Button("移除", role: .destructive) { Task { await save("") } }.controlSize(.small).disabled(busy)
                } else {
                    SecureField("粘贴 API Key，留空即不启用", text: $draft)
                        .textFieldStyle(.roundedBorder).frame(maxWidth: 260)
                        .onSubmit { Task { await save(draft) } }
                    Button("保存") { Task { await save(draft) } }
                        .controlSize(.small).disabled(busy || draft.trimmingCharacters(in: .whitespaces).isEmpty)
                    if editing { Button("取消") { editing = false; draft = "" }.controlSize(.small) }
                }
                if busy { ProgressView().controlSize(.small) }
            }
            if let settings, settings.configured {
                if let credits = settings.credits {
                    Text("已启用 IPQS · 账户剩余额度 \(credits) 次" + (settings.usage.map { " · 已用 \($0) 次" } ?? "") + "。只查新 IP、当前 IP 每天一次和最近 10 个历史 IP。")
                        .font(.system(size: 10)).foregroundStyle(.secondary)
                } else if let error = settings.error {
                    Text("已填写，但暂时无法连接 IPQS：" + error).font(.system(size: 10)).foregroundStyle(.orange)
                }
            }
            if let message { Text(message).font(.system(size: 10)).foregroundStyle(.orange) }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .task { settings = try? await api.ipqsSettings() }
    }

    private func save(_ key: String) async {
        busy = true
        defer { busy = false }
        do {
            settings = try await api.setIPQSKey(key.trimmingCharacters(in: .whitespacesAndNewlines))
            message = nil
            editing = false
            draft = ""
        } catch {
            message = error.localizedDescription
        }
    }
}
