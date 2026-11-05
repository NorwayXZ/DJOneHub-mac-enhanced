import SwiftUI

// MARK: - 一键体检

struct CheckupItem: Codable, Sendable, Identifiable {
    let name: String
    let level: String
    let detail: String
    var id: String { name }
}

struct CheckupModule: Codable, Sendable, Identifiable {
    let id: String?
    let name: String?
    let level: String
    let summary: String
    let items: [CheckupItem]
    let seconds: Double?

    var identity: String { id ?? "module" }
}

struct CheckupReport: Codable, Sendable {
    let modules: [CheckupModule]
}

extension DJOneHubAPI {
    func checkup() async throws -> CheckupReport { try await get(path: "api/checkup", timeout: 40) }
}

private func checkupColor(_ level: String) -> Color {
    switch level {
    case "ok": return .green
    case "warn": return .orange
    case "fail": return .red
    default: return .secondary
    }
}

private func checkupIcon(_ level: String) -> String {
    switch level {
    case "ok": return "checkmark.circle.fill"
    case "warn": return "exclamationmark.triangle.fill"
    case "fail": return "xmark.octagon.fill"
    default: return "minus.circle"
    }
}

/// Checks every module step by step and says, in one line each, what is wrong.
struct CheckupSheet: View {
    let api: DJOneHubAPI
    @Environment(\.dismiss) private var dismiss
    @State private var report: CheckupReport?
    @State private var error: String?
    @State private var started = Date()
    @State private var running = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Label("一键体检", systemImage: "stethoscope").font(.title3.weight(.semibold))
                Spacer()
                Button(running ? "体检中…" : "重新体检") { Task { await run() } }.disabled(running)
                Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            if running {
                BusyBanner(title: "正在逐项检查：模块、卡片、网络、Mac 网卡、代理、上网", started: started,
                           hint: "每个模块同时检查，一般 3–10 秒。")
            }
            if let error {
                Text(error).font(.callout).foregroundStyle(.red)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    ForEach(report?.modules ?? [], id: \.identity) { module in
                        moduleCard(module)
                    }
                }
            }
        }
        .padding(20)
        .frame(minWidth: 520, minHeight: 420)
        .task { await run() }
    }

    private func moduleCard(_ module: CheckupModule) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: checkupIcon(module.level)).foregroundStyle(checkupColor(module.level))
                Text(module.name ?? "蜂窝模块").font(.headline)
                Spacer()
                if let seconds = module.seconds, seconds > 0 {
                    Text(String(format: "%.1f 秒", seconds)).font(.caption).foregroundStyle(.secondary)
                }
            }
            Text(module.summary).font(.callout.weight(.medium)).foregroundStyle(checkupColor(module.level))
                .fixedSize(horizontal: false, vertical: true)
            Divider()
            ForEach(module.items) { item in
                HStack(alignment: .top, spacing: 8) {
                    Image(systemName: checkupIcon(item.level)).foregroundStyle(checkupColor(item.level)).frame(width: 16)
                    Text(item.name).font(.footnote.weight(.semibold)).frame(width: 72, alignment: .leading)
                    Text(item.detail).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 0)
                }
            }
        }
        .padding(14)
        .background(checkupColor(module.level).opacity(0.07), in: RoundedRectangle(cornerRadius: 10))
    }

    private func run() async {
        guard !running else { return }
        running = true
        started = Date()
        error = nil
        defer { running = false }
        do {
            let result = try await api.checkup()
            withAnimation(.easeOut(duration: 0.2)) { report = result }
            let bad = result.modules.filter { $0.level != "ok" }
            Toast.shared.show(bad.isEmpty ? "体检完成：全部正常" : "体检完成：\(bad.count) 个模块需要注意", bad.isEmpty ? .success : .failure)
        } catch {
            self.error = "体检没能完成：" + error.localizedDescription
        }
    }
}
