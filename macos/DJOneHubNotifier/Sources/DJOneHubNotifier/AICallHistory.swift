import Foundation
import SwiftUI

/// One AI-assisted call kept after hang-up: the bilingual captions and summary.
struct AICallRecord: Codable, Identifiable, Equatable {
    struct Line: Codable, Equatable {
        var who: String
        var text: String
        var chinese: String?
    }
    var id: String
    var date: Date
    var number: String?
    var modes: [String]
    var lines: [Line]
    var summary: String?
}

extension AICaption.Speaker {
    var label: String {
        switch self {
        case .them: return "对方"
        case .me: return "我"
        case .ai: return "AI"
        case .note: return "提示"
        }
    }
}

/// Records live in Application Support/DJOneHub/ai-calls, one JSON file each.
enum AICallHistory {
    static let changed = Notification.Name("AICallHistoryChanged")

    private static var dir: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let url = base.appendingPathComponent("DJOneHub/ai-calls", isDirectory: true)
        try? FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }

    private static func file(_ id: String) -> URL {
        let safe = id.map { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" ? $0 : "_" }
        return dir.appendingPathComponent(String(safe) + ".json")
    }

    private static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        return e
    }()

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    static func save(_ record: AICallRecord) {
        guard let data = try? encoder.encode(record) else { return }
        try? data.write(to: file(record.id), options: [.atomic])
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file(record.id).path)
        NotificationCenter.default.post(name: changed, object: nil)
    }

    static func update(_ id: String, _ change: (inout AICallRecord) -> Void) {
        guard let data = try? Data(contentsOf: file(id)),
              var record = try? decoder.decode(AICallRecord.self, from: data) else { return }
        change(&record)
        save(record)
    }

    static func delete(_ id: String) {
        try? FileManager.default.removeItem(at: file(id))
        NotificationCenter.default.post(name: changed, object: nil)
    }

    static func load() -> [AICallRecord] {
        let files = (try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)) ?? []
        return files.filter { $0.pathExtension == "json" }
            .compactMap { try? decoder.decode(AICallRecord.self, from: Data(contentsOf: $0)) }
            .sorted { $0.date > $1.date }
    }
}

/// "AI 通话记录": past AI calls with their captions and summary.
struct AICallHistorySheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var records: [AICallRecord] = []
    @State private var selected: String?

    private static let dateFormat: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "M月d日 HH:mm"
        return f
    }()

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("AI 通话记录").font(.headline)
                Spacer()
                if let id = selected {
                    Button("删除这条", role: .destructive) {
                        AICallHistory.delete(id)
                        selected = nil
                    }
                }
                Button("完成") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            .padding(12)
            Divider()
            if records.isEmpty {
                Text("还没有记录。用过 AI 翻译 / AI 代打 / AI 接听的电话，挂断后会存在这里。")
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                HStack(spacing: 0) {
                    List(records, selection: $selected) { r in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(r.number ?? "未知号码").font(.callout.weight(.semibold))
                            Text("\(Self.dateFormat.string(from: r.date)) · \(r.modes.joined(separator: "、"))")
                                .font(.caption).foregroundStyle(.secondary)
                        }
                        .tag(r.id)
                    }
                    .frame(width: 220)
                    Divider()
                    detail
                }
            }
        }
        .frame(width: 720, height: 520)
        .onAppear(perform: reload)
        .onReceive(NotificationCenter.default.publisher(for: AICallHistory.changed)) { _ in reload() }
    }

    private func reload() {
        records = AICallHistory.load()
        if selected == nil || !records.contains(where: { $0.id == selected }) { selected = records.first?.id }
    }

    @ViewBuilder private var detail: some View {
        if let r = records.first(where: { $0.id == selected }) {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    if let summary = r.summary {
                        VStack(alignment: .leading, spacing: 4) {
                            Label("通话总结", systemImage: "list.bullet.rectangle")
                                .font(.caption.weight(.semibold))
                            Text(summary).font(.callout)
                        }
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 10).fill(Color.accentColor.opacity(0.12)))
                    }
                    ForEach(Array(r.lines.enumerated()), id: \.offset) { _, line in
                        if line.who == "提示" {
                            Text(line.text).font(.caption).foregroundStyle(.secondary)
                        } else {
                            HStack(alignment: .top, spacing: 8) {
                                Text(line.who).font(.caption.weight(.bold)).frame(width: 30, alignment: .leading)
                                VStack(alignment: .leading, spacing: 2) {
                                    if let zh = line.chinese, !zh.isEmpty {
                                        Text(zh).font(.callout)
                                        if !line.text.isEmpty {
                                            Text(line.text).font(.caption).foregroundStyle(.secondary)
                                        }
                                    } else {
                                        Text(line.text).font(.callout)
                                    }
                                }
                            }
                        }
                    }
                }
                .textSelection(.enabled)
                .padding(14)
            }
        } else {
            Text("选一条记录").foregroundStyle(.secondary).frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}
