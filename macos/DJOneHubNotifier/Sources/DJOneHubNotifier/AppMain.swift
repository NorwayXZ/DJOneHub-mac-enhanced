import AppKit
import Foundation
import SwiftUI

@main
enum DJOneHubNotifierMain {
    @MainActor
    static func main() {
        if CommandLine.arguments.contains("--self-test") {
            SelfTest.run()
            return
        }
        let app = NSApplication.shared
        let delegate = AppDelegate(arguments: CommandLine.arguments)
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        app.run()
    }
}

enum SelfTest {
    @MainActor
    static func run() {
        precondition(NotificationText.displayNumber("  ") == "未知号码")
        let codeMessage = SMSMessage(
            sender: "10086",
            content: "您的验证码是 482913",
            code: "482913",
            timestamp: Date()
        )
        precondition(NotificationText.smsPreview(codeMessage) == "验证码 482913")
        let longMessage = SMSMessage(
            sender: "10086",
            content: "第一行\n第二行以及一段很长很长的短信正文",
            code: nil,
            timestamp: Date()
        )
        precondition(NotificationText.smsPreview(longMessage, limit: 8) == "第一行 第二行以…")
        let hidden = SystemNotifications.messageContent(codeMessage, reveal: false)
        precondition(!hidden.body.contains("482913"))
        precondition(!hidden.body.contains(codeMessage.content))
        precondition(hidden.userInfo["destination"] as? String == "messages")
        let visible = SystemNotifications.messageContent(codeMessage, reveal: true)
        precondition(visible.body == codeMessage.content)
        let notificationID = SystemNotifications.identifier(kind: "sms", identity: codeMessage.identity)
        precondition(!notificationID.contains("482913") && !notificationID.contains("10086"))
        precondition(notificationID == SystemNotifications.identifier(kind: "sms", identity: codeMessage.identity))
        precondition(notificationID != SystemNotifications.identifier(kind: "sms", identity: longMessage.identity))
        precondition(AppUpdater.newer("v1.3.0-proxy-preview.2", than: "1.3.0-proxy-preview.1"))
        precondition(AppUpdater.newer("v1.3.0", than: "1.3.0-proxy-preview.2"))
        precondition(!AppUpdater.newer("v1.2.9", than: "1.3.0"))
        precondition(!AppUpdater.newer("v1.3.0", than: "1.3.0"))
        precondition(!AppUpdater.newer("invalid", than: "1.3.0"))
        let fleetCenter = ProxyCenter(api: DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:1/")!))
        fleetCenter.previewOnly = true
        fleetCenter.fleetMode = true
        let firstState = CellularProxyStatus(running: true, address: "127.0.0.1:10909", interface: "demo", publicIP: "192.0.2.10", previousIP: "", phase: "", error: "", connections: 0, uploaded: 0, downloaded: 0)
        fleetCenter.modules = [
            FleetModule(id: "a", name: "A", port: 10909, identity: "demo", connected: true, needsConfirmation: false, generation: "1", interface: "demo", error: "", proxy: firstState),
            FleetModule(id: "b", name: "B", port: 10910, identity: "demo", connected: true, needsConfirmation: true, generation: "2", interface: "demo", error: "", proxy: nil)
        ]
        var summaryRunning = false
        fleetCenter.onStatusChange = { summaryRunning = $0?.running == true }
        fleetCenter.select("b")
        precondition(fleetCenter.status?.address == "127.0.0.1:10910")
        precondition(fleetCenter.unavailable && summaryRunning && fleetCenter.runningCount == 1)
        fleetCenter.select("a")
        precondition(fleetCenter.status?.publicIP == "192.0.2.10")
        print("DJOneHubNotifier self-test passed")
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private let api: DJOneHubAPI
    private let panel: NotifierPanel
    private let gpsMapPanel = GPSMapPanel()
    private let previewMode: String?
    private let snapshotPath: String?
    private let callCenter: CallCenter
    private let contactStore: ContactStore
    private let ringtoneStore: RingtoneStore
    private let appSettings: AppSettings
    private var mainWindow: NSWindow?
    private let proxyCenter: ProxyCenter
    private let bandCenter: BandCenter
    private let usageCenter: UsageCenter
    private let updater: AppUpdater
    private let systemNotifications = SystemNotifications()
    private var appStatusItem: NSStatusItem?
    private var proxyTimer: Timer?
    private let healthCheck: Bool
    private let reviewSafe: Bool

    private var callTimer: Timer?
    private var smsTimer: Timer?
    private var gpsTimer: Timer?
    private var cellularTimer: Timer?
    private var gpsAnimationTimer: Timer?
    private var gpsSearchTimeoutTimer: Timer?
    private var gpsStatusItem: NSStatusItem?
    private var cellularStatusItem: NSStatusItem?
    private var gpsWasEnabled = false
    private var gpsSearchTimedOut = false
    private var gpsStartupFramesRemaining = 0
    private var gpsAnimationFrame = 0
    private var lastActiveCallID: String?
    private var seenCallHistoryIDs = Set<String>()
    private var seenMessageIDs = Set<String>()
    private var initializedCalls = false
    private var initializedMessages = false
    private var consecutiveErrors = 0
    // URLSession may take longer than the timer interval while the module is
    // handling an AT command. Never let an older response overwrite a newer
    // incoming-call state and hide the panel.
    private var callPollInFlight = false
    private var smsPollInFlight = false
    private var wasCallConnected = false

    init(arguments: [String]) {
        let baseURL = Self.argumentValue("--base-url", in: arguments)
            .flatMap(URL.init(string:))
            ?? URL(string: "http://127.0.0.1:7575/")!
        api = DJOneHubAPI(baseURL: baseURL)
        proxyCenter = ProxyCenter(api: api)
        bandCenter = BandCenter(api: api)
        usageCenter = UsageCenter(api: api)
        updater = AppUpdater(api: api)
        previewMode = Self.argumentValue("--preview", in: arguments)
        snapshotPath = Self.argumentValue("--snapshot", in: arguments)
        healthCheck = arguments.contains("--health-check")
        reviewSafe = arguments.contains("--review-safe")
        callCenter = CallCenter(api: api)
        contactStore = ContactStore()
        panel = NotifierPanel()
        panel.contactStore = contactStore
        ringtoneStore = RingtoneStore()
        appSettings = AppSettings()
        panel.appSettings = appSettings
        super.init()
    }

    /// 完全退出会 bootout 后端与守护服务；重新打开应用时把缺失的
    /// LaunchAgent（~/Library/LaunchAgents/<label>.plist）重新引导回来。
    /// notifier 自身不在此注册，避免手动打开时出现双实例。
    private func ensureModuleServices() async {
        let uid = getuid()
        let agentsDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents", isDirectory: true)
        for label in ["com.jamie.djonehub", "com.jamie.djonehub-wifi-sms-guard"] {
            let plist = agentsDir.appendingPathComponent("\(label).plist")
            guard FileManager.default.fileExists(atPath: plist.path) else { continue }
            if Self.serviceIsLoaded(label, uid: uid) { continue }
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            process.arguments = ["bootstrap", "gui/\(uid)", plist.path]
            do {
                try process.run()
                process.waitUntilExit()
            } catch {
                // 忽略：轮询到离线时仍会再次尝试
            }
        }
    }

    /// launchctl kickstart -k 强制重启已加载的后台服务，用于休眠唤醒后
    /// 重新初始化 4G 模块、DHCP 与路由策略，或救活挂死的后端进程。
    private func restartModuleServices() async {
        let uid = getuid()
        for label in ["com.jamie.djonehub", "com.jamie.djonehub-wifi-sms-guard"] {
            guard Self.serviceIsLoaded(label, uid: uid) else { continue }
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            process.arguments = ["kickstart", "-k", "gui/\(uid)/\(label)"]
            do {
                try process.run()
                process.waitUntilExit()
            } catch {
                // 忽略：服务不可用时，轮询到离线会再尝试拉起
            }
        }
    }

    private static func serviceIsLoaded(_ label: String, uid: uid_t) -> Bool {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        process.arguments = ["print", "gui/\(uid)/\(label)"]
        process.standardOutput = Pipe()
        process.standardError = Pipe()
        do {
            try process.run()
            process.waitUntilExit()
            return process.terminationStatus == 0
        } catch {
            return false
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        installMainMenu()
        if healthCheck {
            Task { await runHealthCheck() }
            return
        }
        if let previewMode {
            showPreview(previewMode)
            if let snapshotPath {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
                    guard let self else { return }
                    try? self.panel.saveSnapshot(to: URL(fileURLWithPath: snapshotPath))
                    NSApplication.shared.terminate(nil)
                }
            }
            return
        }
        installStatusMenu()
        systemNotifications.onOpen = { [weak self] destination in
            guard let self else { return }
            self.systemNotifications.destination = destination
            self.showMainWindow()
        }
        if !reviewSafe { systemNotifications.start() }
        bandCenter.onFinish = { [weak self] title, body in self?.systemNotifications.postBandResult(title: title, body: body) }
        usageCenter.onAlert = { [weak self] title, body in self?.systemNotifications.postUsageAlert(title: title, body: body) }
        proxyTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                await self.proxyCenter.refresh()
                await self.bandCenter.refresh()
                await self.usageCenter.refresh()
            }
        }
        if reviewSafe {
            proxyCenter.previewOnly = true
            proxyTimer?.invalidate()
            proxyCenter.status = CellularProxyStatus(running: false, address: "127.0.0.1:10909", interface: "", publicIP: "", previousIP: "", phase: "", error: "预览模式：未连接后台，未操作硬件", connections: 0, uploaded: 0, downloaded: 0)
            if CommandLine.arguments.contains("--review-fleet") {
                proxyCenter.fleetMode = true
                proxyCenter.modules = (0..<3).map { index in
                    let state = CellularProxyStatus(running: index == 0, address: "127.0.0.1:\(10909 + index)", interface: "演示网卡 \(index + 1)", publicIP: index == 0 ? "192.0.2.10" : "", previousIP: "", phase: "", error: "界面演示，未启动真实代理", connections: 0, uploaded: 0, downloaded: 0)
                    return FleetModule(id: "demo-\(index)", name: "演示模块 \(index + 1)", port: 10909 + index, identity: "演示 USB 位置", connected: index != 2, needsConfirmation: index == 1, generation: "demo", interface: "演示网卡", error: "", proxy: state)
                }
                proxyCenter.select("demo-0")
            }
            if CommandLine.arguments.contains("--review-proxy-active") {
                let now = ISO8601DateFormatter().string(from: Date())
                proxyCenter.status = CellularProxyStatus(
                    history: [
                        ProxyIPVisit(ip: "192.0.2.10", firstSeen: now, lastSeen: now, location: "演示归属地", isp: "演示运营商"),
                        ProxyIPVisit(ip: "198.51.100.20", firstSeen: now, lastSeen: now, location: "演示归属地", isp: "演示运营商"),
                        ProxyIPVisit(ip: "192.0.2.10", firstSeen: now, lastSeen: now, location: "演示归属地", isp: "演示运营商")
                    ], location: "演示归属地", isp: "演示运营商", checkedAt: now,
                    running: true, address: "127.0.0.1:10909", interface: "演示接口", publicIP: "192.0.2.10", previousIP: "198.51.100.20", phase: "changed", error: "界面演示：示例 IP，未开启真实代理", connections: 0, uploaded: 0, downloaded: 0)
            }
        } else {
            Task { await proxyCenter.refresh() }
        }
        let demoCall = CommandLine.arguments.contains("--show-call")
        if !demoCall && !reviewSafe {
            callTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
                Task { @MainActor in await self?.pollCalls() }
            }
            smsTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
                Task { @MainActor in await self?.pollMessages() }
            }
            gpsTimer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
                Task { @MainActor in await self?.pollGPSStatus() }
            }
            cellularTimer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in
                Task { @MainActor in await self?.pollCellularStatus() }
            }
            NSWorkspace.shared.notificationCenter.addObserver(
                self,
                selector: #selector(systemDidWake),
                name: NSWorkspace.didWakeNotification,
                object: nil
            )
            Task {
                // 重新打开应用时，恢复被「完全退出」停止的后台服务（后端 + WiFi/短信守护）
                if !reviewSafe { await ensureModuleServices() }
                try? await Task.sleep(nanoseconds: 1_000_000_000)
                await pollCalls()
                await pollMessages()
                await pollGPSStatus()
                await pollCellularStatus()
            }
            callCenter.start()
            if !reviewSafe {
                Task { await contactStore.requestAccess() }
            }
        }
        if demoCall {
            let demo = CallRecord(
                id: "demo-call",
                index: 0,
                direction: "incoming",
                state: "active",
                number: "189 •••• ••••",
                startedAt: Date().addingTimeInterval(-75),
                updatedAt: Date(),
                endedAt: nil,
                missed: false
            )
            callCenter.activeCall = demo
            showMainWindow()
        }
        if CommandLine.arguments.contains("--show-window") {
            showMainWindow()
        } else if !demoCall {
            NSApp.setActivationPolicy(.accessory)
        }
        if previewMode == nil, let snapshotPath {
            DispatchQueue.main.asyncAfter(deadline: .now() + 2.0) { [weak self] in
                guard let self else { return }
                try? self.saveMainWindowSnapshot(to: URL(fileURLWithPath: snapshotPath))
                NSApplication.shared.terminate(nil)
            }
        }
    }

    private func runHealthCheck() async {
        do {
            let calls = try await api.callStatus()
            let messages = try await api.messages()
            print(
                "health-check passed: callPolling=\(calls.polling) " +
                "callHistory=\(calls.history?.count ?? 0) smsCount=\(messages.count)"
            )
            NSApplication.shared.terminate(nil)
        } catch {
            fputs("health-check failed: \(error.localizedDescription)\n", stderr)
            exit(1)
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        proxyTimer?.invalidate()
        callTimer?.invalidate()
        smsTimer?.invalidate()
        gpsTimer?.invalidate()
        cellularTimer?.invalidate()
        stopGPSAnimation()
        cancelGPSSearchTimeout()
        removeGPSStatusItem()
        removeCellularStatusItem()
    }

    private func pollCalls() async {
        guard !callPollInFlight else {
            return
        }
        callPollInFlight = true
        defer { callPollInFlight = false }

        do {
            let status = try await api.callStatus()
            consecutiveErrors = 0
            let history = status.history ?? []

            if !initializedCalls {
                initializedCalls = true
                seenCallHistoryIDs = Set(history.map(\.id))
            }

            if let active = status.active,
               active.direction == "incoming",
               active.state == "incoming" || active.state == "waiting" {
                if active.id != lastActiveCallID {
                    showIncoming(active)
                }
                lastActiveCallID = active.id
            } else {
                if lastActiveCallID != nil {
                    panel.hide()
                }
                ringtoneStore.stopRinging()
                lastActiveCallID = nil
            }

            if let active = status.active {
                if active.state == "active" || active.state == "held" {
                    if !wasCallConnected, !(mainWindow?.isVisible ?? false) {
                        showMainWindow()
                    }
                    wasCallConnected = true
                } else {
                    wasCallConnected = false
                }
            } else {
                if wasCallConnected, !(mainWindow?.isVisible ?? false) {
                    showMainWindow()
                }
                wasCallConnected = false
            }

            let newMissed = history.filter { $0.missed && !seenCallHistoryIDs.contains($0.id) }
            for missed in newMissed {
                seenCallHistoryIDs.insert(missed.id)
                systemNotifications.postMissedCall(missed)
            }
            if lastActiveCallID == nil, let missed = newMissed.first { showMissed(missed) }
            seenCallHistoryIDs.formUnion(history.map(\.id))
        } catch {
            consecutiveErrors += 1
            if consecutiveErrors == 5 {
                await ensureModuleServices()
                await restartModuleServices()
                panel.show(
                    .error(message: error.localizedDescription),
                    onReject: {},
                    onAnswer: {},
                    onOpen: openDJOneHub
                )
            }
        }
    }

    /// 系统从休眠唤醒后，强制重启后台服务并立刻恢复轮询与菜单栏状态，
    /// 避免 4G 模块/网络在唤醒后无法自动找回。
    @objc private func systemDidWake(_ notification: Notification) {
        Task { @MainActor in
            // 等 USB 设备重新枚举、网络栈稳定后再重启服务
            try? await Task.sleep(nanoseconds: 4_000_000_000)
            consecutiveErrors = 0
            await restartModuleServices()
            try? await Task.sleep(nanoseconds: 1_000_000_000)
            await pollCalls()
            await pollMessages()
            await pollGPSStatus()
            await pollCellularStatus()
        }
    }

    private func pollMessages() async {
        guard !smsPollInFlight else {
            return
        }
        smsPollInFlight = true
        defer { smsPollInFlight = false }

        do {
            let messages = try await api.messages()
            if !initializedMessages {
                initializedMessages = true
                seenMessageIDs = Set(messages.map(\.identity))
                return
            }
            let newMessages = messages.filter { !seenMessageIDs.contains($0.identity) }
            seenMessageIDs.formUnion(messages.map(\.identity))
            for message in newMessages { systemNotifications.postMessage(message) }
            if lastActiveCallID == nil, let newest = newMessages.first { showMessage(newest) }
        } catch {
            // Call polling owns the offline warning to avoid duplicate banners.
        }
    }

    private func pollGPSStatus() async {
        guard let status = try? await api.gpsStatus() else { return }
        if status.enabled {
            let signalLevel = Self.gpsSignalLevel(for: status.lastFix)
            if !gpsWasEnabled {
                gpsWasEnabled = true
                gpsSearchTimedOut = false
                startGPSAnimation()
                scheduleGPSSearchTimeout()
                gpsMapPanel.show()
            }
            gpsMapPanel.update(with: status.lastFix)
            if let signalLevel {
                stopGPSAnimation()
                cancelGPSSearchTimeout()
                showGPSStatusItem(signalLevel: signalLevel)
            } else if gpsSearchTimedOut {
                stopGPSAnimation()
                showGPSStatusItem(signalLevel: nil)
            } else if gpsAnimationTimer == nil {
                startGPSAnimation()
            }
        } else {
            gpsWasEnabled = false
            gpsSearchTimedOut = false
            stopGPSAnimation()
            cancelGPSSearchTimeout()
            removeGPSStatusItem()
            gpsMapPanel.hide()
        }
    }

    // Shown whenever the module answers, not only when 4G is the default route:
    // broadband usually stays default while the SIM carries the SOCKS5 proxy.
    private func pollCellularStatus() async {
        guard let modem = try? await api.modemStatus() else {
            removeCellularStatusItem()
            return
        }
        let registered = ["已注册", "漫游注册"].contains(modem.regStatusText ?? "")
        let online = modem.simInserted == true && registered && Self.isCellularNetwork(modem.networkMode)
        let level = online ? Self.cellularSignalLevel(modem.signalDBM ?? -113) : 0
        var detail: String
        if modem.simInserted != true {
            detail = "未检测到 SIM 卡"
        } else if !online {
            detail = "蜂窝网络" + (modem.regStatusText ?? "未注册")
        } else {
            detail = "信号 \(level)/4"
            if let dbm = modem.signalDBM { detail += " · \(dbm) dBm" }
            if let name = modem.operatorName, !name.isEmpty { detail += " · " + name }
        }
        let role = (try? await api.isUsingCellularRoute()) == true ? "4G 正在接管网络" : "4G 供蜂窝代理使用"
        showCellularStatusItem(signalLevel: level, toolTip: "DJOneHub \(role) · \(detail)；点击打开控制面板")
    }

    private func showCellularStatusItem(signalLevel: Int, toolTip: String) {
        if cellularStatusItem == nil {
            cellularStatusItem = NSStatusBar.system.statusItem(withLength: 48)
            cellularStatusItem?.button?.target = self
            cellularStatusItem?.button?.action = #selector(openDJOneHubFromCellularMenuBar)
        }
        cellularStatusItem?.button?.image = Self.cellularStatusImage(signalLevel: signalLevel)
        cellularStatusItem?.button?.toolTip = toolTip
    }

    private func removeCellularStatusItem() {
        guard let cellularStatusItem else { return }
        NSStatusBar.system.removeStatusItem(cellularStatusItem)
        self.cellularStatusItem = nil
    }

    private func showGPSStatusItem(signalLevel: Int?, scanPhase: Int? = nil) {
        if gpsStatusItem == nil {
            gpsStatusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
            gpsStatusItem?.button?.target = self
            gpsStatusItem?.button?.action = #selector(openDJOneHubFromMenuBar)
        }
        gpsStatusItem?.button?.image = Self.gpsStatusImage(signalLevel: signalLevel, scanPhase: scanPhase)
        if signalLevel != nil {
            gpsStatusItem?.button?.toolTip = "DJOneHub GPS 定位已开启；点击展开或收起地图"
        } else if gpsSearchTimedOut {
            gpsStatusItem?.button?.toolTip = "DJOneHub GPS 定位已开启：暂未找到卫星信号；点击展开或收起地图"
        } else {
            gpsStatusItem?.button?.toolTip = "DJOneHub GPS 定位已开启：正在搜索卫星；点击展开或收起地图"
        }
    }

    private func removeGPSStatusItem() {
        guard let gpsStatusItem else { return }
        NSStatusBar.system.removeStatusItem(gpsStatusItem)
        self.gpsStatusItem = nil
    }

    private func startGPSAnimation() {
        guard gpsAnimationTimer == nil else { return }
        gpsStartupFramesRemaining = 8
        gpsAnimationFrame = 0
        renderGPSAnimationFrame()
        gpsAnimationTimer = Timer.scheduledTimer(withTimeInterval: 0.32, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.renderGPSAnimationFrame()
            }
        }
    }

    private func stopGPSAnimation() {
        gpsAnimationTimer?.invalidate()
        gpsAnimationTimer = nil
        gpsStartupFramesRemaining = 0
    }

    private func scheduleGPSSearchTimeout() {
        cancelGPSSearchTimeout()
        gpsSearchTimeoutTimer = Timer.scheduledTimer(withTimeInterval: 120, repeats: false) { [weak self] _ in
            Task { @MainActor in
                guard let self, self.gpsWasEnabled else { return }
                self.gpsSearchTimedOut = true
                self.stopGPSAnimation()
                self.showGPSStatusItem(signalLevel: nil)
            }
        }
    }

    private func cancelGPSSearchTimeout() {
        gpsSearchTimeoutTimer?.invalidate()
        gpsSearchTimeoutTimer = nil
    }

    private func renderGPSAnimationFrame() {
        gpsAnimationFrame += 1
        if gpsStartupFramesRemaining > 0 {
            // First show a neutral, rising four-bar search animation. It is a
            // UI transition only; no position or signal result is fabricated.
            let level = ((gpsAnimationFrame - 1) % 4) + 1
            showGPSStatusItem(signalLevel: level)
            gpsStartupFramesRemaining -= 1
            return
        }
        // Until the module reports a valid fix, use a red rotating scan arc.
        showGPSStatusItem(signalLevel: nil, scanPhase: gpsAnimationFrame % 8)
    }

    @objc private func openDJOneHubFromMenuBar() {
        gpsMapPanel.toggle()
    }

    @objc private func openDJOneHubFromCellularMenuBar() {
        openDJOneHub()
    }

    private static func isCellularNetwork(_ mode: String?) -> Bool {
        let normalized = mode?.uppercased() ?? ""
        return normalized.contains("LTE") || normalized.contains("4G")
    }

    private static func cellularSignalLevel(_ dbm: Int) -> Int {
        if dbm >= -65 { return 4 }
        if dbm >= -75 { return 3 }
        if dbm >= -85 { return 2 }
        return 1
    }

    // A compact stepped mobile-signal indicator that mirrors the iPhone
    // status bar: four signal bars on the left, a "4G" label on the right.
    // The label is vertically centered and sized to the same height as the
    // tallest signal bar (13.8pt). Drawn as a template image so macOS renders
    // it white on a dark menu bar and black on a light one, like iOS.
    private static func cellularStatusImage(signalLevel: Int) -> NSImage {
        let image = NSImage(size: NSSize(width: 48, height: 18))
        image.lockFocus()
        // 模板色：菜单栏深色时系统渲染为白色，浅色时自动变黑，与 iPhone 状态栏一致
        let color = NSColor.black
        let active = color
        let inactive = color.withAlphaComponent(0.30)
        for (index, height) in [4.2, 7.4, 10.6, 13.8].enumerated() {
            (index < signalLevel ? active : inactive).setFill()
            let bar = NSBezierPath(
                roundedRect: NSRect(x: CGFloat(index) * 5.2, y: 1, width: 3.6, height: height),
                xRadius: 0.9,
                yRadius: 0.9
            )
            bar.fill()
        }
        // "4G"：与菜单栏电量百分比同字号（menuBarFont 13pt、SF Pro 常规字重不加粗），垂直居中于信号条
        let label = "4G" as NSString
        label.draw(
            at: NSPoint(x: 22, y: 0.5),
            withAttributes: [
                .font: NSFont.systemFont(ofSize: 13, weight: .regular),
                .foregroundColor: color,
            ]
        )
        image.unlockFocus()
        image.isTemplate = true
        image.accessibilityDescription = "DJOneHub 4G 信号 \(signalLevel) 格"
        return image
    }

    private static func gpsSignalLevel(for fix: GPSFixSummary?) -> Int? {
        guard let fix,
              let satellites = Int(fix.satellites),
              let hdop = Double(fix.hdop),
              satellites >= 4,
              hdop.isFinite
        else { return nil }
        if satellites >= 10 && hdop <= 1.2 { return 4 }
        if satellites >= 8 && hdop <= 2 { return 3 }
        if satellites >= 6 && hdop <= 3.5 { return 2 }
        if hdop <= 5 { return 1 }
        return nil
    }

    // `satellite` is not present in every macOS SF Symbols release. Draw a
    // compact vector icon so the menu-bar indicator is reliable on macOS 13.
    // A missing or weak fix is deliberately red and omits signal bars.
    private static func gpsStatusImage(signalLevel: Int?, scanPhase: Int? = nil) -> NSImage {
        let image = NSImage(size: NSSize(width: 18, height: 18))
        image.lockFocus()
        let weakSignal = signalLevel == nil
        let color: NSColor = weakSignal ? .systemRed : .black
        color.setFill()
        color.setStroke()

        NSBezierPath(ovalIn: NSRect(x: 7, y: 7, width: 4, height: 4)).fill()

        let upperPanel = NSBezierPath()
        upperPanel.move(to: NSPoint(x: 2, y: 12))
        upperPanel.line(to: NSPoint(x: 6.5, y: 10.5))
        upperPanel.line(to: NSPoint(x: 7.5, y: 12.5))
        upperPanel.line(to: NSPoint(x: 3, y: 14))
        upperPanel.close()
        upperPanel.fill()

        let lowerPanel = NSBezierPath()
        lowerPanel.move(to: NSPoint(x: 10.5, y: 5.5))
        lowerPanel.line(to: NSPoint(x: 15, y: 4))
        lowerPanel.line(to: NSPoint(x: 16, y: 6))
        lowerPanel.line(to: NSPoint(x: 11.5, y: 7.5))
        lowerPanel.close()
        lowerPanel.fill()

        let antenna = NSBezierPath()
        antenna.move(to: NSPoint(x: 10, y: 10))
        antenna.line(to: NSPoint(x: 14.5, y: 14.5))
        antenna.lineWidth = 1.3
        antenna.stroke()

        if let signalLevel {
            for level in 1...signalLevel {
                let radius = CGFloat(2) + CGFloat(level) * 1.65
                let wave = NSBezierPath()
                wave.appendArc(withCenter: NSPoint(x: 10, y: 10), radius: radius, startAngle: 30, endAngle: 72, clockwise: false)
                wave.lineWidth = 1.15
                wave.stroke()
            }
        } else if let scanPhase {
            let scan = NSBezierPath()
            let startAngle = CGFloat(scanPhase * 45)
            scan.appendArc(
                withCenter: NSPoint(x: 9, y: 9),
                radius: 7,
                startAngle: startAngle,
                endAngle: startAngle + 78,
                clockwise: false
            )
            scan.lineWidth = 1.35
            scan.stroke()
        }

        image.unlockFocus()
        image.isTemplate = !weakSignal
        image.accessibilityDescription = weakSignal
            ? "DJOneHub GPS 信号弱"
            : "DJOneHub GPS 定位正常"
        return image
    }

    private func showIncoming(_ call: CallRecord) {
        ringtoneStore.startRinging()
        panel.show(
            .incoming(
                number: NotificationText.displayNumber(call.number),
                startedAt: call.startedAt
            ),
            onReject: { [weak self] in
                Task { @MainActor in
                    await self?.rejectCall()
                    self?.showMainWindow()
                }
            },
            onAnswer: { [weak self] in
                Task { @MainActor in
                    self?.callCenter.answer()
                    self?.showMainWindow()
                }
            },
            onOpen: openDJOneHub
        )
    }

    private func showMissed(_ call: CallRecord) {
        ringtoneStore.stopRinging()
        panel.show(
            .missed(
                number: NotificationText.displayNumber(call.number),
                startedAt: call.startedAt
            ),
            onReject: {},
            onAnswer: {},
            onOpen: openDJOneHub
        )
    }

    private func showMessage(_ message: SMSMessage) {
        NSSound(named: "Glass")?.play()
        panel.show(
            .sms(
                sender: message.sender.isEmpty ? "未知发送方" : message.sender,
                preview: systemNotifications.showMessageContents ? NotificationText.smsPreview(message) : "收到一条新短信，打开 App 查看",
                code: systemNotifications.showMessageContents ? message.code : nil
            ),
            onReject: {},
            onAnswer: {},
            onOpen: openDJOneHub
        )
    }

    private func rejectCall() async {
        do {
            _ = try await api.rejectCall()
            panel.hide()
        } catch {
            panel.show(
                .error(message: "拒接失败：\(error.localizedDescription)"),
                onReject: {},
                onAnswer: {},
                onOpen: openDJOneHub
            )
        }
    }

    private func openDJOneHub() {
        showMainWindow()
    }

    private func showPreview(_ mode: String) {
        switch mode {
        case "sms":
            panel.show(
                .sms(sender: "10086", preview: "验证码 482913", code: "482913"),
                onReject: {},
                onAnswer: {},
                onOpen: openDJOneHub
            )
        default:
            panel.show(
                .incoming(number: "189 •••• ••••", startedAt: Date()),
                onReject: {},
                onAnswer: {},
                onOpen: openDJOneHub
            )
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        showMainWindow()
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard sender === mainWindow else { return true }
        sender.orderOut(nil)
        NSApp.setActivationPolicy(.accessory)
        return false
    }

    private func installStatusMenu() {
        NotificationCenter.default.addObserver(self, selector: #selector(quitWorkspace), name: Notification.Name("DJOneHubQuitRequested"), object: nil)
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        item.button?.image = NSImage(systemSymbolName: "antenna.radiowaves.left.and.right", accessibilityDescription: "DJOneHub")
        let menu = NSMenu()
        func add(_ title: String, _ action: Selector) {
            let entry = NSMenuItem(title: title, action: action, keyEquivalent: "")
            entry.target = self
            menu.addItem(entry)
        }
        add("打开蜂窝工作台", #selector(openWorkspace))
        menu.addItem(.separator())
        add("启动蜂窝代理", #selector(startProxy))
        add("停止蜂窝代理", #selector(stopProxy))
        add("复制 SOCKS5 地址", #selector(copyProxy))
        add("检测蜂窝出口 IP", #selector(probeProxy))
        add("重连并尝试换 IP…", #selector(reconnectProxy))
        add("重连并换到新网段…", #selector(reconnectSegment))
        menu.addItem(.separator())
        add("检查软件更新…", #selector(checkUpdates))
        add("完全退出 DJOneHub…", #selector(quitWorkspace))
        item.button?.target = self
        item.button?.action = #selector(statusItemClicked)
        item.button?.sendAction(on: [.leftMouseUp, .rightMouseUp])
        statusContextMenu = menu
        appStatusItem = item
        proxyCenter.onStatusChange = { [weak self] status in self?.updateProxyStatusIcon(status) }
        updateProxyStatusIcon(proxyCenter.status)
    }
    private func updateProxyStatusIcon(_ status: CellularProxyStatus?) {
        let running = status?.running == true
        let reconnecting = status?.phase == "reconnecting"
        let awaiting = !running && status?.autoStart == true
        let symbol = NSImage(systemSymbolName: "antenna.radiowaves.left.and.right", accessibilityDescription: "DJOneHub")
        if running || reconnecting || awaiting {
            let color: NSColor = running && !reconnecting ? .systemGreen : .systemOrange
            let colored = symbol?.withSymbolConfiguration(NSImage.SymbolConfiguration(paletteColors: [color]))
            colored?.isTemplate = false
            appStatusItem?.button?.image = colored
        } else {
            symbol?.isTemplate = true
            appStatusItem?.button?.image = symbol
        }
        let state = reconnecting ? "蜂窝网络重连中" : (running ? (proxyCenter.fleetMode ? "\(proxyCenter.runningCount) 个蜂窝代理已开启" : "蜂窝代理已开启 · 127.0.0.1:10909") : (status == nil ? "蜂窝代理状态未知" : (awaiting ? "蜂窝网络中断，恢复后自动开启代理" : "蜂窝代理已停止")))
        appStatusItem?.button?.toolTip = "DJOneHub · " + state
    }
    private var statusContextMenu: NSMenu?
    @objc private func statusItemClicked() {
        if NSApp.currentEvent?.type == .rightMouseUp, let item = appStatusItem {
            item.menu = statusContextMenu
            item.button?.performClick(nil)
            item.menu = nil
        } else { showMainWindow() }
    }
    @objc private func checkUpdates() {
        showMainWindow()
        NotificationCenter.default.post(name: Notification.Name("DJOneHubShowUpdates"), object: nil)
        Task { await updater.check() }
    }
    @objc private func openWorkspace() { showMainWindow() }
    @objc private func copyProxy() { proxyCenter.copyAddress() }
    @objc private func startProxy() { performProxyAction("start") }
    @objc private func stopProxy() { performProxyAction("stop") }
    @objc private func probeProxy() { performProxyAction("probe") }
    private func performProxyAction(_ action: String, confirm: Bool = false) {
        Task {
            await proxyCenter.act(action, confirm: confirm)
            if proxyCenter.error != nil { showMainWindow() }
        }
    }
    @objc private func reconnectSegment() {
        let hours = UserDefaults.standard.object(forKey: "djonehub.segmentAvoidHours") as? Int ?? 24
        let alert = NSAlert()
        alert.messageText = "重连并换到新网段？"
        alert.informativeText = "将反复开关射频，直到出口落在最近 \(hours) 小时内没用过的网段，最多 4 次（约 1 分钟）；期间代理、短信和来电会中断。"
        alert.addButton(withTitle: "取消")
        alert.addButton(withTitle: "开始")
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertSecondButtonReturn {
            Task {
                await proxyCenter.act("reconnect_segment", confirm: true, avoidHours: hours)
                if proxyCenter.error != nil { showMainWindow() }
            }
        }
    }
    @objc private func reconnectProxy() {
        let alert = NSAlert()
        alert.messageText = "重连蜂窝网络并尝试换 IP？"
        alert.informativeText = "将中断全部代理连接，并暂时影响短信和来电。运营商不保证分配新 IP。"
        alert.addButton(withTitle: "取消")
        alert.addButton(withTitle: "断开并重连")
        NSApp.activate(ignoringOtherApps: true)
        if alert.runModal() == .alertSecondButtonReturn { performProxyAction("reconnect", confirm: true) }
    }
    @objc private func quitWorkspace() {
        let alert = NSAlert()
        alert.messageText = "停止代理并完全退出？"
        alert.informativeText = "将停止 DJOneHub 后台服务；关闭窗口则可以保持运行。"
        alert.addButton(withTitle: "取消")
        alert.addButton(withTitle: "退出")
        if alert.runModal() != .alertSecondButtonReturn { return }
        Task {
            await proxyCenter.refresh()
            if proxyCenter.reconnecting {
                let warning = NSAlert()
                warning.messageText = "请等待蜂窝重连结束后再退出"
                warning.runModal()
                return
            }
            do { _ = try await api.proxyAction("stop") }
            catch {
                if proxyCenter.reconnecting {
                    let warning = NSAlert()
                    warning.messageText = "请等待蜂窝重连结束后再退出"
                    warning.informativeText = "避免无线电在关闭后尚未恢复时退出。"
                    warning.runModal()
                    return
                }
            }
            for label in ["com.jamie.djonehub", "com.jamie.djonehub-wifi-sms-guard", "com.jamie.djonehub-notifier"] {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/bin/launchctl")
                process.arguments = ["bootout", "gui/\(getuid())/\(label)"]
                try? process.run()
            }
            NSApp.terminate(nil)
        }
    }

    private func showMainWindow() {
        NSApp.setActivationPolicy(.regular)
        if mainWindow == nil {
            let rootView = PhoneAppView()
                .environmentObject(callCenter)
                .environmentObject(contactStore)
                .environmentObject(ringtoneStore)
                .environmentObject(appSettings)
                .environmentObject(proxyCenter)
                .environmentObject(bandCenter)
                .environmentObject(usageCenter)
                .environmentObject(updater)
                .environmentObject(systemNotifications)
            let hosting = FirstClickHostingController(rootView: rootView)
            let window = NSWindow(contentViewController: hosting)
            window.title = "DJOneHub"
            window.styleMask = [.titled, .closable, .miniaturizable, .resizable]
            window.titlebarAppearsTransparent = true
            window.titleVisibility = .hidden
            // macOS 26 透明 NSWindow + SwiftUI 存在已知渲染 bug（内容偶发 180° 颠倒），
            // 主窗口改为不透明，圆角与外观全部由 SwiftUI 主体自绘，规避系统级问题。
            window.isOpaque = true
            window.backgroundColor = PhoneStyle.appBackgroundNS
            window.isMovableByWindowBackground = false
            window.setContentSize(NSSize(width: 1040, height: 740))
            window.delegate = self
            window.center()
            window.isReleasedWhenClosed = false
            window.tabbingMode = .disallowed
            mainWindow = window
        }
        mainWindow?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func saveMainWindowSnapshot(to url: URL) throws {
        guard let view = mainWindow?.contentView else { return }
        mainWindow?.displayIfNeeded()
        view.layoutSubtreeIfNeeded()
        guard let bitmap = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return }
        view.cacheDisplay(in: view.bounds, to: bitmap)
        guard let data = bitmap.representation(using: .png, properties: [:]) else { return }
        try data.write(to: url, options: .atomic)
    }

    private func installMainMenu() {
        let mainMenu = NSMenu()

        let appItem = NSMenuItem()
        mainMenu.addItem(appItem)
        let appMenu = NSMenu()
        appMenu.addItem(
            withTitle: "关于 DJOneHub",
            action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)),
            keyEquivalent: ""
        )
        let updateItem = NSMenuItem(title: "检查软件更新…", action: #selector(checkUpdates), keyEquivalent: "")
        updateItem.target = self
        appMenu.addItem(updateItem)
        appMenu.addItem(.separator())
        appMenu.addItem(
            withTitle: "隐藏 DJOneHub",
            action: #selector(NSApplication.hide(_:)),
            keyEquivalent: "h"
        )
        appMenu.addItem(.separator())
        appMenu.addItem(
            withTitle: "退出 DJOneHub",
            action: #selector(quitWorkspace),
            keyEquivalent: "q"
        )
        appMenu.items.last?.target = self
        appItem.submenu = appMenu

        let editItem = NSMenuItem()
        mainMenu.addItem(editItem)
        let editMenu = NSMenu(title: "编辑")
        editMenu.addItem(withTitle: "拷贝", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = editMenu

        NSApp.mainMenu = mainMenu
    }

    private static func argumentValue(_ flag: String, in arguments: [String]) -> String? {
        guard let index = arguments.firstIndex(of: flag), arguments.indices.contains(index + 1) else {
            return nil
        }
        return arguments[index + 1]
    }
}

// Inactive utility windows should execute the first click, not just gain focus.
final class FirstClickHostingView<Content: View>: NSHostingView<Content> {
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
}
final class FirstClickHostingController<Content: View>: NSHostingController<Content> {
    override func loadView() { view = FirstClickHostingView(rootView: rootView) }
}
