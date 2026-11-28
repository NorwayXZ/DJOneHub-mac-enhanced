import AppKit
import Foundation
import SwiftUI

// MARK: - Structured call summary

/// Structured Chinese summary maintained both live during a call and after hang-up.
struct StructuredCallSummary: Codable, Equatable {
    var topic: String
    var highlights: [String]
    var agreed: [String]
    var openQuestions: [String]
    var nextSteps: [String]
    var updatedAt: Date

    enum CodingKeys: String, CodingKey {
        case topic
        case highlights
        case agreed
        case openQuestions = "open_questions"
        case nextSteps = "next_steps"
        case updatedAt = "updated_at"
    }

    var isEmpty: Bool {
        topic.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && highlights.isEmpty
            && agreed.isEmpty
            && openQuestions.isEmpty
            && nextSteps.isEmpty
    }

    /// Plain-text Chinese summary used for notifications, copy, and legacy `summary` field.
    var plainText: String {
        var parts: [String] = []
        let t = topic.trimmingCharacters(in: .whitespacesAndNewlines)
        if !t.isEmpty { parts.append("主题：\(t)") }
        if !highlights.isEmpty {
            parts.append("已谈重点：\n" + highlights.map { "• \($0)" }.joined(separator: "\n"))
        }
        if !agreed.isEmpty {
            parts.append("已敲定：\n" + agreed.map { "• \($0)" }.joined(separator: "\n"))
        }
        if !openQuestions.isEmpty {
            parts.append("待确认：\n" + openQuestions.map { "• \($0)" }.joined(separator: "\n"))
        }
        if !nextSteps.isEmpty {
            parts.append("下一步：\n" + nextSteps.map { "• \($0)" }.joined(separator: "\n"))
        }
        return parts.joined(separator: "\n\n")
    }

    /// Concise notification body (2–3 lines).
    var notificationBody: String {
        var lines: [String] = []
        if !topic.isEmpty { lines.append("主题：\(topic)") }
        if let firstAgreed = agreed.first { lines.append("已定：\(firstAgreed)") }
        else if let firstHighlight = highlights.first { lines.append("重点：\(firstHighlight)") }
        if let firstNext = nextSteps.first { lines.append("下一步：\(firstNext)") }
        return lines.isEmpty ? plainText : lines.joined(separator: "\n")
    }

    /// Parses a JSON response from the text model (tolerating markdown fences or prose fallback).
    static func parse(from raw: String, updatedAt: Date = Date()) -> StructuredCallSummary? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }

        // Extract JSON object if wrapped in ```json ... ``` or surrounding text.
        if let start = trimmed.firstIndex(of: "{"), let end = trimmed.lastIndex(of: "}"), start < end {
            let jsonSlice = String(trimmed[start ... end])
            if let data = jsonSlice.data(using: .utf8),
               let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                func cleanString(_ v: Any?) -> String {
                    (v as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                }
                func cleanList(_ v: Any?) -> [String] {
                    if let arr = v as? [String] {
                        return arr.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
                    }
                    if let arr = v as? [Any] {
                        return arr.compactMap { ($0 as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) }
                            .filter { !$0.isEmpty }
                    }
                    if let s = v as? String, !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        return [s.trimmingCharacters(in: .whitespacesAndNewlines)]
                    }
                    return []
                }
                let summary = StructuredCallSummary(
                    topic: cleanString(obj["topic"]),
                    highlights: cleanList(obj["highlights"]),
                    agreed: cleanList(obj["agreed"]),
                    openQuestions: cleanList(obj["open_questions"] ?? obj["openQuestions"]),
                    nextSteps: cleanList(obj["next_steps"] ?? obj["nextSteps"]),
                    updatedAt: updatedAt
                )
                if !summary.isEmpty { return summary }
            }
        }

        // Fallback: split bullet lines if model returned plain text.
        let lines = trimmed.components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .map { $0.replacingOccurrences(of: #"^[-•*·\d\.、\s]+"#, with: "", options: .regularExpression) }
            .filter { !$0.isEmpty }
        guard !lines.isEmpty else { return nil }
        return StructuredCallSummary(
            topic: lines[0],
            highlights: Array(lines.dropFirst()),
            agreed: [],
            openQuestions: [],
            nextSteps: [],
            updatedAt: updatedAt
        )
    }
}

// MARK: - Record model (backward-compatible with existing ai-calls/*.json)

/// One AI-assisted call kept after hang-up: bilingual chat log + structured summary.
struct AICallRecord: Codable, Identifiable, Equatable {
    struct Line: Codable, Equatable {
        var who: String
        var text: String
        var chinese: String?
        /// For "我" lines in translate mode: the foreign translation sent to the caller.
        var translation: String?
        var timestamp: Date?
    }
    var id: String
    var date: Date
    var number: String?
    var modes: [String]
    var lines: [Line]
    var summary: String?
    var durationSeconds: Int?
    var language: String?
    var structuredSummary: StructuredCallSummary?

    var topicPreview: String {
        if let topic = structuredSummary?.topic, !topic.isEmpty { return topic }
        if let first = summary?.components(separatedBy: .newlines).first(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) {
            return first
        }
        if let line = lines.first(where: { $0.who != "提示" }) {
            return line.chinese ?? line.text
        }
        return "暂无摘要"
    }

    var durationText: String? {
        guard let secs = durationSeconds, secs > 0 else { return nil }
        if secs >= 3600 {
            return String(format: "%d:%02d:%02d", secs / 3600, (secs % 3600) / 60, secs % 60)
        }
        if secs >= 60 {
            return String(format: "%d分%02d秒", secs / 60, secs % 60)
        }
        return "\(secs)秒"
    }
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

// MARK: - Storage & Export

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
        let path = file(record.id)
        try? data.write(to: path, options: [.atomic])
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
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

    /// Finds past records for the same phone number (normalized exact or last-7-digit match).
    static func records(matchingNumber number: String?, excludingID: String? = nil) -> [AICallRecord] {
        guard let number else { return [] }
        let target = ContactStore.normalized(number)
        guard !target.isEmpty else { return [] }
        let suffix = target.count >= 7 ? String(target.suffix(7)) : ""
        return load().filter { record in
            if let excludingID, record.id == excludingID { return false }
            guard let n = record.number else { return false }
            let norm = ContactStore.normalized(n)
            if norm == target { return true }
            return !suffix.isEmpty && norm.count >= 7 && norm.hasSuffix(suffix)
        }
    }

    static func latestRecord(matchingNumber number: String?, excludingID: String? = nil) -> AICallRecord? {
        records(matchingNumber: number, excludingID: excludingID).first
    }

    /// Exports one call record into clean, shareable Markdown.
    static func exportMarkdown(for record: AICallRecord, contactName: String) -> String {
        let df = DateFormatter()
        df.dateFormat = "yyyy-MM-dd HH:mm"
        let tf = DateFormatter()
        tf.dateFormat = "HH:mm:ss"

        var out: [String] = []
        out.append("# 通话纪要：\(contactName)")
        var meta = ["- 时间：\(df.string(from: record.date))"]
        if let number = record.number, !number.isEmpty, number != contactName {
            meta.append("- 号码：\(number)")
        }
        if let lang = record.language, !lang.isEmpty {
            meta.append("- 客户语言：\(lang)")
        }
        if let dur = record.durationText {
            meta.append("- 时长：\(dur)")
        }
        if !record.modes.isEmpty {
            meta.append("- 模式：\(record.modes.joined(separator: "、"))")
        }
        out.append(meta.joined(separator: "\n"))

        if let s = record.structuredSummary, !s.isEmpty {
            out.append("## 谈话概要\n\n" + s.plainText)
        } else if let s = record.summary, !s.isEmpty {
            out.append("## 通话总结\n\n" + s)
        }

        var logLines: [String] = ["## 完整双语对话记录"]
        for line in record.lines {
            let stamp = line.timestamp.map { "[\(tf.string(from: $0))] " } ?? ""
            if line.who == "提示" {
                logLines.append("- _\(stamp)\(line.text)_")
            } else if line.who == "对方" {
                if let zh = line.chinese, !zh.isEmpty {
                    logLines.append("- **\(stamp)对方**：\(zh)" + (line.text.isEmpty ? "" : "\n  > 原文：\(line.text)"))
                } else {
                    logLines.append("- **\(stamp)对方**：\(line.text)")
                }
            } else if line.who == "我" {
                let tr = (line.translation ?? "").isEmpty ? "" : "\n  > 译文：\(line.translation!)"
                logLines.append("- **\(stamp)我**：\(line.text)\(tr)")
            } else {
                let zh = (line.chinese ?? "").isEmpty ? "" : "\n  > 中文：\(line.chinese!)"
                logLines.append("- **\(stamp)\(line.who)**：\(line.text)\(zh)")
            }
        }
        out.append(logLines.joined(separator: "\n"))
        return out.joined(separator: "\n\n") + "\n"
    }

    /// Highlights amounts, percentages, dates, times, and key numbers in orange bold
    /// so critical business figures stand out at a glance.
    static func highlightKeyFacts(_ text: String, highlightColor: Color = .orange) -> AttributedString {
        var result = AttributedString(text)
        guard !text.isEmpty else { return result }
        let pattern = #"(?:[$€£¥￥]\s?\d[\d,]*(?:\.\d+)?(?:\s?[kKmMbB万亿])?|\b\d[\d,]*(?:\.\d+)?\s?(?:%|USD|EUR|GBP|RMB|CNY|元|块|美金|美元|欧元|英镑|万|亿|个|件|台|套|箱|吨|kg|KG|天|周|个月|小时|分钟|点|号|日|月|年)|\b\d{1,2}:\d{2}\b|\b\d{4}[-/]\d{1,2}[-/]\d{1,2}\b|\b\d{4,}\b)"#
        guard let regex = try? NSRegularExpression(pattern: pattern) else { return result }
        let matches = regex.matches(in: text, range: NSRange(text.startIndex..., in: text))
        for match in matches {
            guard let range = Range(match.range, in: text),
                  let lower = AttributedString.Index(range.lowerBound, within: result),
                  let upper = AttributedString.Index(range.upperBound, within: result) else { continue }
            result[lower ..< upper].foregroundColor = highlightColor
            result[lower ..< upper].font = .callout.weight(.semibold)
        }
        return result
    }
}

// MARK: - Reusable Summary & Chat Bubble Views

/// Clean structured summary card used both live during calls and in history.
struct StructuredSummaryCard: View {
    let summary: StructuredCallSummary?
    var fallbackText: String? = nil
    var title: String = "谈话概要"
    var isUpdating: Bool = false
    var compact: Bool = false
    var onRefresh: (() -> Void)? = nil
    var onCopy: (() -> Void)? = nil

    private static let timeFmt: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss"
        return f
    }()

    var body: some View {
        VStack(alignment: .leading, spacing: compact ? 8 : 10) {
            HStack(spacing: 6) {
                Label(title, systemImage: "doc.text.magnifyingglass")
                    .font(compact ? .caption.weight(.bold) : .callout.weight(.semibold))
                    .foregroundStyle(Color.accentColor)
                if isUpdating {
                    ProgressView().controlSize(.mini)
                    Text("整理中…").font(.caption2).foregroundStyle(.secondary)
                } else if let updated = summary?.updatedAt {
                    Text("更新于 \(Self.timeFmt.string(from: updated))")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let onCopy, (summary != nil || (fallbackText?.isEmpty == false)) {
                    Button {
                        onCopy()
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.caption)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
                    .help("复制谈话概要")
                }
                if let onRefresh {
                    Button {
                        onRefresh()
                    } label: {
                        Label(compact ? "更新" : "重新整理", systemImage: "arrow.clockwise")
                            .font(.caption)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.mini)
                    .disabled(isUpdating)
                }
            }

            if let s = summary, !s.isEmpty {
                if !s.topic.isEmpty {
                    HStack(alignment: .top, spacing: 6) {
                        Text("主题")
                            .font(.caption2.weight(.bold))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Capsule().fill(Color.accentColor.opacity(0.16)))
                            .foregroundStyle(Color.accentColor)
                        Text(AICallHistory.highlightKeyFacts(s.topic))
                            .font(compact ? .callout.weight(.semibold) : .body.weight(.semibold))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                if !s.highlights.isEmpty {
                    section(icon: "checkmark.bubble.fill", color: .blue, heading: "已谈重点", items: s.highlights)
                }
                if !s.agreed.isEmpty {
                    section(icon: "handshake.fill", color: .green, heading: "已敲定", items: s.agreed)
                }
                if !s.openQuestions.isEmpty {
                    section(icon: "questionmark.circle.fill", color: .orange, heading: "待确认", items: s.openQuestions)
                }
                if !s.nextSteps.isEmpty {
                    section(icon: "arrow.right.circle.fill", color: .purple, heading: "下一步", items: s.nextSteps)
                }
            } else if let text = fallbackText, !text.isEmpty {
                Text(AICallHistory.highlightKeyFacts(text))
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text(isUpdating ? "正在根据刚才的对话整理要点…" : "聊上几句后，AI 会自动把前面讲过的内容总结在这里，方便你随时扫一眼。")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .textSelection(.enabled)
        .padding(compact ? 10 : 14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .fill(Color.accentColor.opacity(0.08))
                .overlay(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .strokeBorder(Color.accentColor.opacity(0.22), lineWidth: 0.7)
                )
        )
    }

    @ViewBuilder
    private func section(icon: String, color: Color, heading: String, items: [String]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.caption2)
                    .foregroundStyle(color)
                Text(heading)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                HStack(alignment: .top, spacing: 6) {
                    Text("•").foregroundStyle(color).font(.caption.weight(.bold))
                    Text(AICallHistory.highlightKeyFacts(item))
                        .font(compact ? .caption : .callout)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }
}

/// Phone-style symmetric bilingual chat bubble for call logs.
struct CallChatBubble: View {
    let who: String
    let text: String
    let chinese: String?
    let translation: String?
    let timestamp: Date?
    var live: Bool = false

    private static let timeFmt: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss"
        return f
    }()

    private var isMine: Bool { who == "我" || who == "AI" }

    var body: some View {
        if who == "提示" {
            HStack {
                Spacer(minLength: 12)
                Text(text)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 3)
                    .background(Capsule().fill(Color.primary.opacity(0.06)))
                Spacer(minLength: 12)
            }
            .padding(.vertical, 2)
        } else {
            HStack(alignment: .bottom, spacing: 8) {
                if isMine { Spacer(minLength: 40) }
                VStack(alignment: isMine ? .trailing : .leading, spacing: 3) {
                    HStack(spacing: 5) {
                        Text(who == "对方" ? "客户" : who)
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(who == "对方" ? Color.blue : (who == "AI" ? Color.purple : PhoneStyle.green))
                        if let timestamp {
                            Text(Self.timeFmt.string(from: timestamp))
                                .font(.caption2.monospacedDigit())
                                .foregroundStyle(.tertiary)
                        }
                        if live {
                            Text("正在说…")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        if who == "对方" {
                            if let zh = chinese, !zh.isEmpty {
                                Text(AICallHistory.highlightKeyFacts(zh))
                                    .font(.callout)
                                    .fixedSize(horizontal: false, vertical: true)
                                if !text.isEmpty {
                                    Text(text)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                            } else {
                                Text(text.isEmpty ? "…" : text)
                                    .font(.callout)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        } else if who == "我" {
                            Text(AICallHistory.highlightKeyFacts(text.isEmpty ? "…" : text))
                                .font(.callout)
                                .fixedSize(horizontal: false, vertical: true)
                            if let tr = translation, !tr.isEmpty {
                                Text("→ " + tr)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        } else {
                            // AI agent speaking on user's behalf
                            if let zh = chinese, !zh.isEmpty {
                                Text(AICallHistory.highlightKeyFacts(zh))
                                    .font(.callout)
                                    .fixedSize(horizontal: false, vertical: true)
                                if !text.isEmpty {
                                    Text(text)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                            } else {
                                Text(text.isEmpty ? "…" : text)
                                    .font(.callout)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(
                        RoundedRectangle(cornerRadius: 14, style: .continuous)
                            .fill(bubbleFill)
                    )
                    .opacity(live ? 0.72 : 1)
                    .textSelection(.enabled)
                }
                if !isMine { Spacer(minLength: 40) }
            }
        }
    }

    private var bubbleFill: Color {
        switch who {
        case "我": return PhoneStyle.green.opacity(0.16)
        case "AI": return Color.purple.opacity(0.14)
        default: return Color.primary.opacity(0.065)
        }
    }
}

// MARK: - Full History & Summary Workspace (embeddable in Recents tab + Settings sheet)

struct AICallHistoryView: View {
    @EnvironmentObject private var contacts: ContactStore
    @State private var records: [AICallRecord] = []
    @State private var selectedID: String?
    @State private var searchText = ""
    @State private var regeneratingID: String?
    var initialSelectedID: String? = nil

    private static let dateFormat: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "M月d日 HH:mm"
        return f
    }()

    private var filteredRecords: [AICallRecord] {
        let q = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return records }
        return records.filter { r in
            let name = contacts.displayName(for: r.number).lowercased()
            if name.contains(q) { return true }
            if (r.number ?? "").lowercased().contains(q) { return true }
            if (r.language ?? "").lowercased().contains(q) { return true }
            if (r.summary ?? "").lowercased().contains(q) { return true }
            if let s = r.structuredSummary, s.plainText.lowercased().contains(q) { return true }
            return r.lines.contains {
                $0.text.lowercased().contains(q)
                    || ($0.chinese ?? "").lowercased().contains(q)
                    || ($0.translation ?? "").lowercased().contains(q)
            }
        }
    }

    private var selectedRecord: AICallRecord? {
        filteredRecords.first(where: { $0.id == selectedID })
            ?? records.first(where: { $0.id == selectedID })
    }

    var body: some View {
        VStack(spacing: 0) {
            if records.isEmpty {
                VStack(spacing: 10) {
                    Image(systemName: "text.bubble")
                        .font(.system(size: 32))
                        .foregroundStyle(.secondary)
                    Text("还没有 AI 谈话记录")
                        .font(.headline)
                    Text("打完带 AI 同传、AI 代打或 AI 接听的电话，双语聊天记录和 AI 谈话概要都会自动保存在这里。")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .frame(maxWidth: 380)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                HStack(spacing: 0) {
                    // Left column: search + call list
                    VStack(spacing: 0) {
                        HStack(spacing: 6) {
                            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                            TextField("搜客户、号码、谈过的内容…", text: $searchText)
                                .textFieldStyle(.plain)
                            if !searchText.isEmpty {
                                Button { searchText = "" } label: {
                                    Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        .padding(.horizontal, 10)
                        .padding(.vertical, 7)
                        .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.06)))
                        .padding(10)

                        Divider()

                        List(filteredRecords, selection: $selectedID) { r in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack(spacing: 6) {
                                    Text(contacts.displayName(for: r.number))
                                        .font(.callout.weight(.semibold))
                                        .lineLimit(1)
                                    Spacer()
                                    if let lang = r.language, !lang.isEmpty {
                                        Text(lang)
                                            .font(.caption2)
                                            .padding(.horizontal, 5)
                                            .padding(.vertical, 1)
                                            .background(Capsule().fill(Color.accentColor.opacity(0.14)))
                                            .foregroundStyle(Color.accentColor)
                                    }
                                }
                                Text(r.topicPreview)
                                    .font(.caption)
                                    .foregroundStyle(.primary.opacity(0.82))
                                    .lineLimit(2)
                                HStack(spacing: 6) {
                                    Text(Self.dateFormat.string(from: r.date))
                                    if let dur = r.durationText {
                                        Text("· " + dur)
                                    }
                                    Spacer()
                                    Text("\(r.lines.filter { $0.who != "提示" }.count) 句")
                                }
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 4)
                            .tag(r.id)
                        }
                        .listStyle(.sidebar)
                    }
                    .frame(width: 265)

                    Divider()

                    // Right column: structured summary + full bilingual chat bubbles
                    detailView
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .onAppear {
            reload()
            if let initialSelectedID, records.contains(where: { $0.id == initialSelectedID }) {
                selectedID = initialSelectedID
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: AICallHistory.changed)) { _ in reload() }
    }

    private func reload() {
        records = AICallHistory.load()
        if selectedID == nil || !records.contains(where: { $0.id == selectedID }) {
            selectedID = records.first?.id
        }
    }

    @ViewBuilder private var detailView: some View {
        if let r = selectedRecord {
            let contactName = contacts.displayName(for: r.number)
            VStack(spacing: 0) {
                // Header bar
                HStack(spacing: 10) {
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text(contactName).font(.headline)
                            if let lang = r.language, !lang.isEmpty {
                                Text("客户语言：\(lang)")
                                    .font(.caption2)
                                    .padding(.horizontal, 6)
                                    .padding(.vertical, 2)
                                    .background(Capsule().fill(Color.accentColor.opacity(0.14)))
                                    .foregroundStyle(Color.accentColor)
                            }
                        }
                        HStack(spacing: 6) {
                            if let num = r.number, !num.isEmpty, num != contactName {
                                Text(num)
                                Text("·")
                            }
                            Text(Self.dateFormat.string(from: r.date))
                            if let dur = r.durationText {
                                Text("· 时长 \(dur)")
                            }
                            if !r.modes.isEmpty {
                                Text("· \(r.modes.joined(separator: "、"))")
                            }
                        }
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button {
                        copySummary(r)
                    } label: {
                        Label("复制概要", systemImage: "doc.on.doc")
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(r.structuredSummary == nil && (r.summary ?? "").isEmpty)

                    Button {
                        exportRecord(r, contactName: contactName)
                    } label: {
                        Label("导出纪要", systemImage: "square.and.arrow.up")
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)

                    Button(role: .destructive) {
                        AICallHistory.delete(r.id)
                        selectedID = nil
                    } label: {
                        Image(systemName: "trash")
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .help("删除这条记录")
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)

                Divider()

                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        StructuredSummaryCard(
                            summary: r.structuredSummary,
                            fallbackText: r.summary,
                            title: "AI 谈话概要",
                            isUpdating: regeneratingID == r.id,
                            compact: false,
                            onRefresh: { regenerate(r) },
                            onCopy: { copySummary(r) }
                        )

                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Label("完整双语聊天记录", systemImage: "bubble.left.and.bubble.right")
                                    .font(.caption.weight(.semibold))
                                    .foregroundStyle(.secondary)
                                Spacer()
                                Text("共 \(r.lines.filter { $0.who != "提示" }.count) 条对话")
                                    .font(.caption2)
                                    .foregroundStyle(.tertiary)
                            }
                            ForEach(Array(r.lines.enumerated()), id: \.offset) { _, line in
                                CallChatBubble(
                                    who: line.who,
                                    text: line.text,
                                    chinese: line.chinese,
                                    translation: line.translation,
                                    timestamp: line.timestamp
                                )
                            }
                        }
                    }
                    .padding(16)
                }
            }
        } else {
            Text("在左侧选一条通话记录查看概要和对话")
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func copySummary(_ record: AICallRecord) {
        let text = record.structuredSummary?.plainText ?? record.summary ?? ""
        guard !text.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        Toast.shared.show("谈话概要已复制", .success)
    }

    private func exportRecord(_ record: AICallRecord, contactName: String) {
        let md = AICallHistory.exportMarkdown(for: record, contactName: contactName)
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.plainText]
        let df = DateFormatter()
        df.dateFormat = "yyyyMMdd-HHmm"
        let safeName = contactName.replacingOccurrences(of: "/", with: "-")
        panel.nameFieldStringValue = "通话纪要-\(safeName)-\(df.string(from: record.date)).md"
        if panel.runModal() == .OK, let url = panel.url {
            do {
                try md.write(to: url, atomically: true, encoding: .utf8)
                Toast.shared.show("通话纪要已导出", .success)
            } catch {
                Toast.shared.show("导出失败：\(error.localizedDescription)", .failure)
            }
        }
    }

    private func regenerate(_ record: AICallRecord) {
        guard regeneratingID == nil else { return }
        regeneratingID = record.id
        Task {
            if let structured = await AICallAssistant.summarizeRecord(record) {
                AICallHistory.update(record.id) {
                    $0.structuredSummary = structured
                    $0.summary = structured.plainText
                }
                Toast.shared.show("谈话概要已重新整理", .success)
            } else {
                Toast.shared.show("整理总结失败，请检查 AI 设置与网络", .failure)
            }
            regeneratingID = nil
        }
    }
}

/// Sheet wrapper used from Settings -> AI 通话助手 -> AI 通话记录.
struct AICallHistorySheet: View {
    @Environment(\.dismiss) private var dismiss
    var initialSelectedID: String? = nil

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Label("AI 谈话记录与概要", systemImage: "text.bubble.fill")
                    .font(.headline)
                Spacer()
                Button("完成") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
            .padding(12)
            Divider()
            AICallHistoryView(initialSelectedID: initialSelectedID)
        }
        .frame(width: 880, height: 580)
    }
}
