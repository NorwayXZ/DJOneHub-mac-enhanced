import SwiftUI

struct FleetModeSettings: View {
    @EnvironmentObject private var proxy: ProxyCenter
    @State private var enabled = false
    @State private var available = false
    @State private var busy = false
    @State private var confirm = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label(enabled ? "多模块模式已开启" : "当前使用单模块模式", systemImage: "externaldrive.connected.to.line.below")
                    .font(.headline)
                Spacer()
                Button(enabled ? "切回单模块模式" : "启用多模块模式") { confirm = true }
                    .disabled(busy || !available || proxy.previewOnly)
            }
            Text("每台模块使用独立后台和固定 SOCKS5 端口，从 10909 开始分配。设备断开后保留端口，不自动换用其他 SIM。")
                .font(.caption).foregroundStyle(.secondary)
            Text("插上第二个模块时会自动切到多模块模式。所有功能对每个模块都可用：左上角切换模块，拨号、短信发送、频段、流量、呼叫转移等页面针对选中的模块；来电、短信列表和推送覆盖全部模块，并标明来自哪个模块。Mac 只有一套麦克风和扬声器，同一时间只能在 Mac 上接听一路通话。")
                .font(.caption).foregroundStyle(.secondary)
            if busy { ProgressView("正在切换并重启后台…").controlSize(.small) }
            if let error { Text(error).font(.caption).foregroundStyle(.orange) }
        }.padding(14)
            .task { await refresh() }
            .alert(enabled ? "切回单模块模式？" : "启用多模块模式？", isPresented: $confirm) {
                Button("取消", role: .cancel) {}
                Button("确认切换", role: .destructive) { Task { await change() } }
            } message: {
                Text("将重启 DJOneHub 后台并断开代理连接；有通话或模块操作时会拒绝切换。没有可靠序列号的模块，接入后需确认绑定。")
            }
    }
    private func refresh() async {
        guard !proxy.previewOnly else { enabled = proxy.fleetMode; return }
        do {
            let mode = try await proxy.api.moduleMode()
            enabled = mode.enabled
            available = mode.switchAvailable
        } catch { self.error = "后台暂不支持切换，或当前并非已安装服务。" }
    }
    private func change() async {
        guard !busy, !proxy.previewOnly else { return }
        let target = !enabled
        busy = true
        error = nil
        defer { busy = false }
        do {
            try await proxy.api.switchModuleMode(target)
            for _ in 0..<30 {
                try? await Task.sleep(nanoseconds: 500_000_000)
                if let mode = try? await proxy.api.moduleMode(), mode.enabled == target {
                    enabled = target
                    available = mode.switchAvailable
                    await proxy.refresh()
                    return
                }
            }
            self.error = "后台正在重启，请稍后重新打开设置检查模式。"
        } catch { self.error = error.localizedDescription }
    }
}
