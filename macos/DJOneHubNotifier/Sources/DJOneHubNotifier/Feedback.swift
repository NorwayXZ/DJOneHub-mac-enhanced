import SwiftUI

// MARK: - 操作反馈：浮动提示、进行中横幅、掉网诊断

/// Why the module has no network, in plain words (from the backend).
struct NetworkIssue: Codable, Sendable, Equatable {
    let level: String
    let title: String
    let detail: String
    let since: String?

    var sinceDate: Date? {
        guard let since else { return nil }
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: since) ?? ISO8601DateFormatter().date(from: since)
    }
}

/// A short message that floats over every page, so the result of a button is
/// visible wherever the user has scrolled to.
@MainActor
final class Toast: ObservableObject {
    enum Kind { case success, failure, progress, info }
    struct Item: Identifiable, Equatable {
        let id = UUID()
        let text: String
        let kind: Kind
    }

    static let shared = Toast()
    @Published private(set) var item: Item?
    private var hideTask: Task<Void, Never>?

    /// Progress messages stay until replaced; others hide after a few seconds.
    func show(_ text: String, _ kind: Kind = .info) {
        let item = Item(text: text, kind: kind)
        withAnimation(.spring(response: 0.3, dampingFraction: 0.85)) { self.item = item }
        hideTask?.cancel()
        guard kind != .progress else { return }
        hideTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(kind == .failure ? 7 : 4))
            guard !Task.isCancelled, self?.item?.id == item.id else { return }
            withAnimation(.easeOut(duration: 0.25)) { self?.item = nil }
        }
    }

    func dismiss() {
        hideTask?.cancel()
        withAnimation(.easeOut(duration: 0.2)) { item = nil }
    }
}

struct ToastOverlay: View {
    @ObservedObject private var toast = Toast.shared

    var body: some View {
        VStack {
            Spacer()
            if let item = toast.item {
                HStack(spacing: 10) {
                    switch item.kind {
                    case .progress: ProgressView().controlSize(.small)
                    case .success: Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                    case .failure: Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    case .info: Image(systemName: "info.circle.fill").foregroundStyle(Color.accentColor)
                    }
                    Text(item.text)
                        .font(.callout)
                        .fixedSize(horizontal: false, vertical: true)
                        .frame(maxWidth: 460, alignment: .leading)
                    Button { toast.dismiss() } label: { Image(systemName: "xmark") }
                        .buttonStyle(.plain).foregroundStyle(.secondary)
                }
                .padding(.horizontal, 16).padding(.vertical, 12)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
                .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(.quaternary))
                .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
                .padding(.bottom, 22)
                .transition(.move(edge: .bottom).combined(with: .opacity))
                .id(item.id)
            }
        }
        .frame(maxWidth: .infinity)
        .allowsHitTesting(toast.item != nil)
    }
}

/// "正在…" with a spinner and a running seconds counter.
struct BusyBanner: View {
    let title: String
    let started: Date
    var hint: String? = nil

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            ProgressView().controlSize(.small)
            VStack(alignment: .leading, spacing: 2) {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    Text(title + " · \(max(0, Int(context.date.timeIntervalSince(started)))) 秒")
                        .font(.footnote.weight(.semibold))
                }
                if let hint {
                    Text(hint).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
        .transition(.opacity)
    }
}

/// Explains why the module has no network and whose problem it is.
struct NetworkIssueBanner: View {
    let issue: NetworkIssue

    private var color: Color {
        switch issue.level {
        case "error": return .red
        case "warn": return .orange
        default: return .accentColor
        }
    }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            if issue.level == "info" {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: issue.level == "error" ? "xmark.octagon.fill" : "exclamationmark.triangle.fill")
                    .foregroundStyle(color)
            }
            VStack(alignment: .leading, spacing: 3) {
                TimelineView(.periodic(from: .now, by: 1)) { context in
                    Text(issue.title + elapsed(context.date)).font(.footnote.weight(.semibold))
                }
                Text(issue.detail).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(color.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
    }

    private func elapsed(_ now: Date) -> String {
        guard let since = issue.sinceDate else { return "" }
        let seconds = max(0, Int(now.timeIntervalSince(since)))
        return seconds < 60 ? " · \(seconds) 秒" : " · 已 \(seconds / 60) 分钟"
    }
}
