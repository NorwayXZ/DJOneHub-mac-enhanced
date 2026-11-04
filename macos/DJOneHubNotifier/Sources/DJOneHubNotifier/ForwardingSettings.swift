import SwiftUI

struct SMSForwardSettings: Decodable {
    var telegramConfigured: Bool
    var telegramTokenHint: String? = nil
    var telegramChatID: String? = nil
    var telegramAPI: String? = nil
    var wechatConfigured: Bool
    var wechatService: String? = nil
    var wechatKeyHint: String? = nil
    enum CodingKeys: String, CodingKey {
        case telegramConfigured = "telegram_configured"
        case telegramTokenHint = "telegram_token_hint"
        case telegramChatID = "telegram_chat_id"
        case telegramAPI = "telegram_api"
        case wechatConfigured = "wechat_configured"
        case wechatService = "wechat_service"
        case wechatKeyHint = "wechat_key_hint"
    }
}

struct CCFCEntry: Decodable, Identifiable {
    var reason: Int
    var active: Bool
    var number: String? = nil
    var time: Int? = nil
    var id: Int { reason }
}

struct CallForwardStatus: Decodable {
    struct Saved: Decodable { var mode: String; var number: String }
    var iccid: String
    var entries: [CCFCEntry]? = nil
    var saved: Saved? = nil
    var error: String? = nil
}

extension DJOneHubAPI {
    func smsForwardSettings() async throws -> SMSForwardSettings {
        try await get(path: "api/forwarding/sms")
    }
    func saveSMSForward(_ body: [String: String], test: Bool = false) async throws -> SMSForwardSettings {
        struct Body: Encodable {
            var channel: String
            var test: Bool
            var telegram_token, telegram_chat_id, telegram_api, wechat_service, wechat_key: String?
        }
        let payload = Body(channel: body["channel"] ?? "", test: test,
                           telegram_token: body["telegram_token"], telegram_chat_id: body["telegram_chat_id"], telegram_api: body["telegram_api"],
                           wechat_service: body["wechat_service"], wechat_key: body["wechat_key"])
        // A test message may go out over 4G after broadband times out.
        return try await postDecoded(path: "api/forwarding/sms", body: payload, timeout: 40)
    }
    func callForwardStatus() async throws -> CallForwardStatus {
        try await get(path: "api/forwarding/calls", timeout: 200)
    }
    func setCallForward(action: String, mode: String = "", number: String = "") async throws -> CallForwardStatus {
        struct Body: Encodable { var action, mode, number: String }
        return try await postDecoded(path: "api/forwarding/calls", body: Body(action: action, mode: mode, number: number), timeout: 400)
    }
}

/// 设置 → 短信转发：新短信推送到 Telegram 和/或微信（PushPlus、Server酱），可以同时开。
struct SMSForwardPanel: View {
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var settings: SMSForwardSettings?
    @State private var editing: String?
    @State private var token = ""
    @State private var chatID = ""
    @State private var telegramAPI = ""
    @State private var service = "pushplus"
    @State private var key = ""
    @State private var busy: String?
    @State private var message: (String, Bool)?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("模块收到新短信后推送到你的手机，验证码放在标题最前面。两个渠道可以同时开。")
                .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            channel("Telegram", configured: settings?.telegramConfigured == true,
                    summary: [settings?.telegramTokenHint, settings?.telegramChatID.map { "Chat ID " + $0 }].compactMap { $0 }.joined(separator: " · "),
                    id: "telegram") {
                SecureField("机器人 Token（在 @BotFather 创建机器人后得到）", text: $token).textFieldStyle(.roundedBorder)
                TextField("Chat ID（给 @userinfobot 发消息可查到）", text: $chatID).textFieldStyle(.roundedBorder)
                TextField("接口地址（可不填；连不上 Telegram 时填反代地址）", text: $telegramAPI).textFieldStyle(.roundedBorder)
                Text("先在 Telegram 里给你的机器人发一句话，它才能给你发消息。宽带连不上 Telegram 时会自动改走 4G 发送。")
                    .font(.system(size: 10)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } save: {
                ["channel": "telegram", "telegram_token": token, "telegram_chat_id": chatID, "telegram_api": telegramAPI]
            }
            channel("微信", configured: settings?.wechatConfigured == true,
                    summary: [settings?.wechatService == "serverchan" ? "Server酱" : "PushPlus", settings?.wechatKeyHint].compactMap { $0 }.joined(separator: " · "),
                    id: "wechat") {
                Picker("推送服务", selection: $service) {
                    Text("PushPlus（pushplus.plus）").tag("pushplus")
                    Text("Server酱（sct.ftqq.com）").tag("serverchan")
                }.pickerStyle(.radioGroup)
                SecureField(service == "serverchan" ? "SendKey" : "Token", text: $key).textFieldStyle(.roundedBorder)
                Text("在对应网站用微信扫码登录、关注公众号后复制 Token / SendKey，短信会由该公众号推送到微信。")
                    .font(.system(size: 10)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } save: {
                ["channel": "wechat", "wechat_service": service, "wechat_key": key]
            }
            if let (text, ok) = message {
                Text(text).font(.system(size: 11)).foregroundStyle(ok ? Color.green : Color.orange).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .task { settings = try? await api.smsForwardSettings() }
    }

    @ViewBuilder
    private func channel<Fields: View>(_ title: String, configured: Bool, summary: String, id: String,
                                       @ViewBuilder fields: () -> Fields, save: @escaping () -> [String: String]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: configured ? "checkmark.circle.fill" : "circle").foregroundStyle(configured ? Color.green : Color.secondary)
                Text(title).font(.system(size: 13, weight: .medium))
                if configured && editing != id {
                    Text(summary).font(.system(.caption, design: .monospaced)).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 4)
                if busy == id { ProgressView().controlSize(.small) }
                if editing == id {
                    Button("取消") { editing = nil }.controlSize(.small)
                } else if configured {
                    Button("发测试") { Task { await run(id, ["channel": id], test: true) } }.controlSize(.small).disabled(busy != nil)
                    Button("修改") { begin(id) }.controlSize(.small)
                    Button("关闭", role: .destructive) { Task { await run(id, ["channel": id]) } }.controlSize(.small).disabled(busy != nil)
                } else {
                    Button("设置") { begin(id) }.controlSize(.small)
                }
            }
            if editing == id {
                fields()
                HStack {
                    Spacer()
                    Button("保存并发测试消息") { Task { await run(id, save()) } }
                        .buttonStyle(.borderedProminent).controlSize(.small).disabled(busy != nil)
                }
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
    }

    private func begin(_ id: String) {
        token = ""; key = ""
        chatID = settings?.telegramChatID ?? ""
        telegramAPI = settings?.telegramAPI ?? ""
        service = settings?.wechatService ?? "pushplus"
        message = nil
        editing = id
    }

    private func run(_ id: String, _ body: [String: String], test: Bool = false) async {
        busy = id
        defer { busy = nil }
        do {
            settings = try await api.saveSMSForward(body, test: test)
            let enabled = id == "telegram" ? settings?.telegramConfigured == true : settings?.wechatConfigured == true
            message = (test ? "测试消息已发出，请看手机" : (enabled ? "已保存，测试消息已发出，请看手机" : "已关闭"), true)
            editing = nil
        } catch {
            message = (error.localizedDescription, false)
        }
    }
}

/// 设置 → 呼叫转移：在运营商网络里设置，Mac 关机也照样转到你的手机。
struct CallForwardPanel: View {
    private let api = DJOneHubAPI(baseURL: URL(string: "http://127.0.0.1:7575/")!)
    @State private var status: CallForwardStatus?
    @State private var number = ""
    @State private var mode = "all"
    @State private var busy = false
    @State private var loaded = false
    @State private var message: String?

    private static let reasonNames = [0: "全部来电", 1: "占线时", 2: "无人接听时", 3: "关机或无信号时"]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("在运营商网络里设置转移：来电直接打到你填的手机号，Mac 关机或模块离线也照样转。费用按这张卡的运营商规则计算，漫游时转到国内号码可能按国际长途收费。")
                .font(.system(size: 11)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let status {
                current(status)
            } else if loaded {
                Text("读取失败，点“刷新”重试").font(.caption).foregroundStyle(.orange)
            } else {
                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("正在向运营商查询当前设置…").font(.caption).foregroundStyle(.secondary) }
            }
            HStack(spacing: 8) {
                TextField("转到的号码，例如 +8613800138000", text: $number).textFieldStyle(.roundedBorder).frame(maxWidth: 240)
                Picker("", selection: $mode) {
                    Text("全部来电都转").tag("all")
                    Text("没接 / 占线 / 关机时才转").tag("conditional")
                }.labelsHidden().frame(maxWidth: 200)
                Button("开启转移") { Task { await act("set") } }
                    .buttonStyle(.borderedProminent).controlSize(.small)
                    .disabled(busy || number.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            HStack(spacing: 8) {
                if status?.saved != nil {
                    Button("关闭转移，恢复原设置", role: .destructive) { Task { await act("clear") } }.controlSize(.small).disabled(busy)
                }
                Button("刷新") { Task { await load() } }.controlSize(.small).disabled(busy)
                if busy { ProgressView().controlSize(.small); Text("正在和运营商通信，约 10–60 秒…").font(.caption).foregroundStyle(.secondary) }
            }
            Text("国内号码加 +86，例如 +8613800138000。开启前会保存这张卡原有的转移（如语音信箱），关闭时原样恢复。换卡后按新卡分别设置。")
                .font(.system(size: 10)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if let message { Text(message).font(.system(size: 11)).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true) }
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .task { await load() }
    }

    @ViewBuilder
    private func current(_ status: CallForwardStatus) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if let saved = status.saved {
                Label("已开启：" + (saved.mode == "all" ? "全部来电" : "没接 / 占线 / 关机时") + " 转到 " + saved.number, systemImage: "phone.arrow.right")
                    .font(.system(size: 12, weight: .medium)).foregroundStyle(.green)
            }
            ForEach(status.entries ?? []) { entry in
                HStack(spacing: 6) {
                    Text(Self.reasonNames[entry.reason] ?? "").font(.caption).foregroundStyle(.secondary).frame(width: 96, alignment: .leading)
                    Text(entry.active ? "转到 " + (entry.number ?? "") + (entry.reason == 2 && (entry.time ?? 0) > 0 ? "（响 \(entry.time!) 秒后）" : "") : "未转移")
                        .font(.system(.caption, design: .monospaced)).foregroundStyle(entry.active ? .primary : .secondary)
                }
            }
            if let error = status.error { Text(error).font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true) }
        }
    }

    private func load() async {
        busy = true
        defer { busy = false; loaded = true }
        do {
            status = try await api.callForwardStatus()
            if number.isEmpty, let saved = status?.saved { number = saved.number; mode = saved.mode }
            message = nil
        } catch {
            message = error.localizedDescription
        }
    }

    private func act(_ action: String) async {
        busy = true
        defer { busy = false }
        do {
            status = try await api.setCallForward(action: action, mode: mode, number: number)
            message = nil
        } catch {
            message = error.localizedDescription
        }
    }
}
