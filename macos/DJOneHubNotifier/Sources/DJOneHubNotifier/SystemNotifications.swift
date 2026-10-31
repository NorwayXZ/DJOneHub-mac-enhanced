import AppKit
import CryptoKit
import SwiftUI
import UserNotifications

@MainActor
final class SystemNotifications: NSObject, ObservableObject, UNUserNotificationCenterDelegate {
    @Published var enabled = UserDefaults.standard.object(forKey: "djonehub.systemNotifications") as? Bool ?? true {
        didSet {
            UserDefaults.standard.set(enabled, forKey: "djonehub.systemNotifications")
            if enabled { Task { await requestPermission() } }
        }
    }
    @Published var showMessageContents = UserDefaults.standard.bool(forKey: "djonehub.notificationContents") {
        didSet {
            UserDefaults.standard.set(showMessageContents, forKey: "djonehub.notificationContents")
            // Previously delivered previews must not survive a change to hidden content.
            if !showMessageContents { Task { await removeSMSPreviews() } }
        }
    }
    @Published private(set) var permissionText = "尚未检查通知权限"
    @Published private(set) var lastError: String?
    @Published private(set) var testStatus: String?
    @Published var destination = "proxy"
    var onOpen: ((String) -> Void)?
    private var active = false
    private var permissionTask: Task<Bool, Never>?

    func start() {
        active = true
        UNUserNotificationCenter.current().delegate = self
        Task { await requestPermission() }
    }

    func refreshPermission() async {
        guard active else { permissionText = "预览模式：未请求系统权限"; return }
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        switch settings.authorizationStatus {
        case .notDetermined: permissionText = "尚未授权，请点击允许通知"
        case .denied: permissionText = "系统已拒绝通知，请在系统设置中允许 DJOneHub 通知"
        case .authorized, .provisional:
            if settings.notificationCenterSetting != .enabled {
                permissionText = "已授权，但通知中心展示已关闭，请在系统设置中开启"
            } else if settings.alertSetting != .enabled {
                permissionText = "通知中心已开启，横幅已关闭"
            } else { permissionText = "已允许通知中心和横幅提醒" }
        default: permissionText = "请检查系统通知设置"
        }
    }

    @discardableResult
    private func authorize() async -> Bool {
        guard active, enabled else { return false }
        if let task = permissionTask { return await task.value }
        let task = Task { @MainActor [weak self] in
            guard let self else { return false }
            let center = UNUserNotificationCenter.current()
            let settings = await center.notificationSettings()
            if settings.authorizationStatus == .notDetermined {
                do { return try await center.requestAuthorization(options: [.alert]) }
                catch { self.lastError = error.localizedDescription; return false }
            }
            return settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional
        }
        permissionTask = task
        let granted = await task.value
        permissionTask = nil
        return granted
    }

    func requestPermission() async {
        _ = await authorize()
        await refreshPermission()
    }

    func openSystemSettings() {
        guard let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension") else { return }
        NSWorkspace.shared.open(url)
    }

    static func identifier(kind: String, identity: String) -> String {
        let digest = SHA256.hash(data: Data(identity.utf8)).map { String(format: "%02x", $0) }.joined()
        return "djonehub.\(kind).\(digest)"
    }

    static func messageContent(_ message: SMSMessage, reveal: Bool) -> UNMutableNotificationContent {
        let content = UNMutableNotificationContent()
        content.title = "新短信"
        content.subtitle = message.sender.isEmpty ? "未知发送方" : message.sender
        content.body = reveal ? String(message.content.prefix(240)) : "收到一条新短信，点击打开 DJOneHub 查看。"
        content.categoryIdentifier = "DJONEHUB_SMS"
        content.threadIdentifier = "djonehub.sms"
        content.userInfo = ["destination": "messages"]
        return content
    }

    func postMessage(_ message: SMSMessage) {
        Task {
            guard await authorize(), enabled else { return }
            // Read the privacy setting after permission is resolved, not before it.
            let content = Self.messageContent(message, reveal: showMessageContents)
            await deliver(id: Self.identifier(kind: "sms", identity: message.identity), content: content)
        }
    }

    func postMissedCall(_ call: CallRecord) {
        Task {
            guard await authorize(), enabled else { return }
            let content = UNMutableNotificationContent()
            content.title = "未接来电"
            content.body = NotificationText.displayNumber(call.number)
            content.categoryIdentifier = "DJONEHUB_MISSED"
            content.threadIdentifier = "djonehub.calls"
            content.userInfo = ["destination": "recents"]
            await deliver(id: Self.identifier(kind: "missed", identity: call.id), content: content)
        }
    }

    func postBandResult(title: String, body: String) {
        Task {
            guard await authorize(), enabled else { return }
            let content = UNMutableNotificationContent()
            content.title = title
            content.body = body
            content.userInfo = ["destination": "settings"]
            await deliver(id: "djonehub.band", content: content)
        }
    }

    func sendTest() {
        Task {
            testStatus = "正在发送测试通知…"
            guard await authorize(), enabled else {
                testStatus = "尚未发送，请先开启通知权限"
                await refreshPermission()
                return
            }
            let content = UNMutableNotificationContent()
            content.title = "DJOneHub 通知测试"
            content.body = "系统通知已接通。新短信和未接来电将显示在通知中心。"
            content.userInfo = ["destination": "settings"]
            guard await deliver(id: "djonehub.test", content: content) else { testStatus = "测试通知发送失败"; return }
            try? await Task.sleep(nanoseconds: 300_000_000)
            let delivered = await UNUserNotificationCenter.current().deliveredNotifications()
            testStatus = delivered.contains { $0.request.identifier == "djonehub.test" }
                ? "已确认测试通知进入 macOS 通知中心"
                : "测试通知已提交，请检查通知中心和专注模式设置"
        }
    }

    @discardableResult
    private func deliver(id: String, content: UNMutableNotificationContent) async -> Bool {
        // Existing app reminders own sounds, avoiding a second simultaneous chime.
        do {
            try await UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id, content: content, trigger: nil))
            lastError = nil
            return true
        } catch { lastError = error.localizedDescription; return false }
    }

    private func removeSMSPreviews() async {
        guard active else { return }
        let center = UNUserNotificationCenter.current()
        let delivered = await center.deliveredNotifications()
        center.removeDeliveredNotifications(withIdentifiers: delivered.filter { $0.request.content.categoryIdentifier == "DJONEHUB_SMS" }.map { $0.request.identifier })
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list])
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        let destination = response.notification.request.content.userInfo["destination"] as? String ?? "messages"
        let open = response.actionIdentifier == UNNotificationDefaultActionIdentifier
        Task { @MainActor [weak self] in
            if open { self?.onOpen?(destination) }
            completionHandler()
        }
    }
}

struct SystemNotificationSettings: View {
    @EnvironmentObject private var notifications: SystemNotifications
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Toggle("通知中心：新短信和未接来电", isOn: $notifications.enabled)
            Toggle("在通知中显示短信内容和验证码", isOn: $notifications.showMessageContents)
            Text("默认隐藏短信正文和验证码；此设置也适用于 App 短信弹窗。来电接听窗口继续保留。")
                .font(.caption).foregroundStyle(.secondary)
            Text(notifications.permissionText).font(.caption).foregroundStyle(.secondary)
            HStack {
                Button("允许通知") { Task { await notifications.requestPermission() } }.disabled(!notifications.enabled)
                Button("打开系统通知设置", action: notifications.openSystemSettings)
                Button("发送测试通知", action: notifications.sendTest).disabled(!notifications.enabled)
            }
            if let error = notifications.lastError { Text(error).font(.caption).foregroundStyle(.orange) }
            if let status = notifications.testStatus { Text(status).font(.caption).foregroundStyle(.secondary) }
        }.padding(14)
            .task { await notifications.refreshPermission() }
            .onReceive(NSWorkspace.shared.notificationCenter.publisher(for: NSWorkspace.didActivateApplicationNotification)) { _ in
                Task { await notifications.refreshPermission() }
            }
    }
}
