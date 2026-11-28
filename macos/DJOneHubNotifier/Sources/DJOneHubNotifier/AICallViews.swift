import AppKit
import SwiftUI

// MARK: - Dial with AI

/// Before dialing: tell the AI what you want, it makes the call.
struct AIDialSheet: View {
    @EnvironmentObject private var calls: CallCenter
    @EnvironmentObject private var aiSettings: AICallSettings
    @Environment(\.dismiss) private var dismiss
    let number: String
    @State private var goal = ""
    @State private var languageID = "English"

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label("AI 替你打电话", systemImage: "sparkles")
                .font(.title3.weight(.semibold))
            Text("拨打 \(number)。AI 会用对方的语言跟他聊，你在屏幕上看中文字幕和实时概要，随时可以点“我来接”。")
                .font(.callout)
                .foregroundStyle(.secondary)
            Text("你要办的事（用中文写清楚）")
                .font(.headline)
            TextEditor(text: $goal)
                .font(.body)
                .frame(height: 110)
                .padding(6)
                .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.06)))
                .overlay(alignment: .topLeading) {
                    if goal.isEmpty {
                        Text("例如：问这家供应商 500 件的报价和交期，报我们公司名，留这个号码。")
                            .foregroundStyle(.tertiary)
                            .padding(12)
                            .allowsHitTesting(false)
                    }
                }
            Picker("先用哪种语言开口", selection: $languageID) {
                ForEach(CallLanguage.all) { Text($0.name).tag($0.id) }
            }
            if !aiSettings.isConfigured {
                Label("先到 设置 › AI 通话助手 填 API Key", systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
            }
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button {
                    calls.dialWithAI(number, goal: goal.trimmingCharacters(in: .whitespacesAndNewlines),
                                     language: CallLanguage.find(languageID))
                    dismiss()
                } label: {
                    Label("让 AI 拨打", systemImage: "phone.fill")
                }
                .keyboardShortcut(.defaultAction)
                .disabled(goal.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !aiSettings.isConfigured)
            }
        }
        .padding(20)
        .frame(width: 440)
        .onAppear { languageID = CallLanguage.preferred(for: number).id }
    }
}

/// During a call: what should the AI say for you.
struct AIGoalSheet: View {
    @EnvironmentObject private var calls: CallCenter
    @Environment(\.dismiss) private var dismiss
    @State private var goal = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("让 AI 接着替你说", systemImage: "sparkles")
                .font(.title3.weight(.semibold))
            Text("用中文写下这通电话要办成什么。AI 会接过话头，你听得到双方，随时点“我来接”。")
                .font(.callout)
                .foregroundStyle(.secondary)
            TextEditor(text: $goal)
                .frame(height: 100)
                .padding(6)
                .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.06)))
            HStack {
                Spacer()
                Button("取消") { dismiss() }
                Button("开始") {
                    calls.startAIAgent(goal: goal.trimmingCharacters(in: .whitespacesAndNewlines))
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(goal.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}

// MARK: - Compact launch panel (shown before AI is turned on)

struct AICallPanel: View {
    @EnvironmentObject private var calls: CallCenter
    @EnvironmentObject private var ai: AICallAssistant
    @EnvironmentObject private var aiSettings: AICallSettings
    let connected: Bool
    @State private var askingGoal = false

    var body: some View {
        VStack(spacing: 10) {
            if let error = ai.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
            }
            if let prev = ai.previousCall {
                PreviousCallBanner(record: prev)
                    .frame(maxWidth: 480)
            }
            if connected {
                HStack(spacing: 10) {
                    CallLanguagePickerView()
                    Button {
                        calls.startAITranslate()
                    } label: {
                        Label(aiSettings.usesSimultaneous ? "开启 AI 同传" : "开启 AI 翻译",
                              systemImage: "waveform.and.mic")
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(PhoneStyle.green)

                    Button {
                        askingGoal = true
                    } label: {
                        Label("AI 替我说", systemImage: "sparkles")
                    }
                    .buttonStyle(.bordered)
                }
                .disabled(!aiSettings.isConfigured)

                if !aiSettings.isConfigured {
                    Text("在 设置 › AI 通话助手 填好 API Key 后可用")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            } else if ai.pendingTranslate {
                Label("接通后自动开启 AI 同声传译（客户语言：\(ai.language.name)）", systemImage: "waveform")
                    .font(.caption)
                    .foregroundStyle(PhoneStyle.green)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 5)
                    .background(Capsule().fill(PhoneStyle.green.opacity(0.12)))
            }
        }
        .sheet(isPresented: $askingGoal) { AIGoalSheet() }
    }
}

// MARK: - Full live split workspace (Left: bilingual chat log, Right: live rolling summary)

struct AICallWorkspaceView: View {
    @EnvironmentObject private var calls: CallCenter
    @EnvironmentObject private var ai: AICallAssistant
    @EnvironmentObject private var aiSettings: AICallSettings
    let connected: Bool
    @State private var askingGoal = false
    @State private var showPreviousDetails = false

    var body: some View {
        VStack(spacing: 10) {
            // Top live controls ribbon
            controlBar

            if let error = ai.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
            }

            // Main two-column business call dashboard
            HStack(alignment: .top, spacing: 14) {
                // LEFT: Live symmetric bilingual chat log
                VStack(spacing: 0) {
                    HStack {
                        Label("实时双语对话", systemImage: "bubble.left.and.bubble.right.fill")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                        Spacer()
                        Text("\(ai.captions.filter { $0.speaker != .note }.count) 条")
                            .font(.caption2.monospacedDigit())
                            .foregroundStyle(.tertiary)
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    Divider()
                    ScrollViewReader { proxy in
                        ScrollView {
                            LazyVStack(alignment: .leading, spacing: 10) {
                                if ai.captions.isEmpty {
                                    Text(ai.isSimultaneousActive
                                         ? "同声传译已就绪：你直接说中文，客户那边 1–2 秒内就能听到\(ai.language.name)；客户开口后中文也会实时显示在这里。"
                                         : "等待对话开始…")
                                        .font(.callout)
                                        .foregroundStyle(.secondary)
                                        .padding(.top, 20)
                                        .frame(maxWidth: .infinity, alignment: .center)
                                }
                                ForEach(ai.captions) { c in
                                    CallChatBubble(
                                        who: c.speaker.label,
                                        text: c.text,
                                        chinese: c.chinese,
                                        translation: c.translation,
                                        timestamp: c.timestamp,
                                        live: c.live
                                    )
                                    .id(c.id)
                                }
                                Color.clear.frame(height: 1).id("chat-bottom")
                            }
                            .padding(12)
                        }
                        .onChange(of: ai.captions.count) { _ in
                            withAnimation(.easeOut(duration: 0.15)) {
                                proxy.scrollTo("chat-bottom", anchor: .bottom)
                            }
                        }
                        .onChange(of: ai.captions.last?.text.count ?? 0) { _ in
                            proxy.scrollTo("chat-bottom", anchor: .bottom)
                        }
                        .onChange(of: ai.captions.last?.chinese?.count ?? 0) { _ in
                            proxy.scrollTo("chat-bottom", anchor: .bottom)
                        }
                        .onChange(of: ai.captions.last?.translation?.count ?? 0) { _ in
                            proxy.scrollTo("chat-bottom", anchor: .bottom)
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(
                    RoundedRectangle(cornerRadius: 14, style: .continuous)
                        .fill(Color.primary.opacity(0.035))
                        .overlay(
                            RoundedRectangle(cornerRadius: 14, style: .continuous)
                                .strokeBorder(Color.primary.opacity(0.09), lineWidth: 0.7)
                        )
                )

                // RIGHT: Live rolling business summary + previous call brief
                ScrollView {
                    VStack(alignment: .leading, spacing: 10) {
                        StructuredSummaryCard(
                            summary: ai.liveSummary,
                            fallbackText: ai.summary,
                            title: "实时谈话概要",
                            isUpdating: ai.isSummarizing,
                            compact: true,
                            onRefresh: { ai.refreshLiveSummaryNow() },
                            onCopy: copyLiveSummary
                        )

                        if let err = ai.summaryError {
                            Text(err)
                                .font(.caption2)
                                .foregroundStyle(.orange)
                        }

                        if let prev = ai.previousCall {
                            previousCallCard(prev)
                        }
                    }
                }
                .frame(minWidth: 270, idealWidth: 310, maxWidth: 340, maxHeight: .infinity)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .sheet(isPresented: $askingGoal) { AIGoalSheet() }
    }

    private var controlBar: some View {
        VStack(spacing: 6) {
            HStack(spacing: 8) {
                if !ai.phase.isEmpty {
                    Label(ai.phase, systemImage: ai.directMicPassthrough ? "mic.fill" : "waveform")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(ai.directMicPassthrough ? Color.orange : PhoneStyle.green)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 4)
                        .background(
                            Capsule().fill((ai.directMicPassthrough ? Color.orange : PhoneStyle.green).opacity(0.14))
                        )
                }

                if ai.mode == .translate {
                    CallLanguagePickerView()

                    // Headset full-duplex toggle
                    Button {
                        ai.setHeadsetMode(!aiSettings.headsetMode)
                    } label: {
                        Label(aiSettings.headsetMode ? "耳机全双工" : "外放防回声",
                              systemImage: aiSettings.headsetMode ? "headphones" : "speaker.wave.2")
                            .font(.caption)
                    }
                    .buttonStyle(.bordered)
                    .tint(aiSettings.headsetMode ? PhoneStyle.green : .secondary)
                    .controlSize(.small)
                    .help(aiSettings.headsetMode
                          ? "已开耳机模式：边听中文边说话不闭麦"
                          : "外放防回声中；戴上耳机后点此切换为全双工")

                    // Caller original monitor volume
                    Menu {
                        Button("原声静音 (0%)") { ai.setCallerMonitorVolume(0.0) }
                        Button("轻背景原声 (18% 推荐)") { ai.setCallerMonitorVolume(0.18) }
                        Button("较清晰原声 (40%)") { ai.setCallerMonitorVolume(0.40) }
                    } label: {
                        Label(monitorVolumeLabel, systemImage: "ear")
                            .font(.caption)
                    }
                    .menuStyle(.borderedButton)
                    .controlSize(.small)
                    .help("调节客户原声在你耳机里的背景音量")

                    // Direct raw voice passthrough toggle
                    Button {
                        ai.setDirectMicPassthrough(!ai.directMicPassthrough)
                    } label: {
                        Label(ai.directMicPassthrough ? "原声直说中" : "直说原声",
                              systemImage: ai.directMicPassthrough ? "mic.fill" : "mic.badge.plus")
                            .font(.caption)
                    }
                    .buttonStyle(.bordered)
                    .tint(ai.directMicPassthrough ? .orange : .secondary)
                    .controlSize(.small)
                    .help("点开后直接把你的原声传给客户（例如直接说 Yes/OK 或报英文拼写），再点一次恢复同传")

                    Button {
                        ai.clearOutboundQueue()
                    } label: {
                        Image(systemName: "forward.end")
                            .font(.caption)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .help("跳过尚未播完的外语队列，立刻跟上新话")
                } else if !ai.isOn && connected {
                    CallLanguagePickerView()
                    Button {
                        calls.startAITranslate()
                    } label: {
                        Label("重新开启 AI 同传", systemImage: "waveform.and.mic")
                            .font(.caption)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(PhoneStyle.green)
                    .controlSize(.small)
                    .disabled(!aiSettings.isConfigured)

                    Button {
                        askingGoal = true
                    } label: {
                        Label("AI 替我说", systemImage: "sparkles")
                            .font(.caption)
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                    .disabled(!aiSettings.isConfigured)
                }

                Spacer()
            }
            if !ai.status.isEmpty {
                Text(ai.status)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var monitorVolumeLabel: String {
        if aiSettings.callerMonitorVolume <= 0.01 { return "原声静音" }
        if aiSettings.callerMonitorVolume >= 0.30 { return "原声40%" }
        return "原声18%"
    }

    private func copyLiveSummary() {
        let text = ai.liveSummary?.plainText ?? ai.summary ?? ""
        guard !text.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        Toast.shared.show("实时谈话概要已复制", .success)
    }

    @ViewBuilder
    private func previousCallCard(_ record: AICallRecord) -> some View {
        let df = DateFormatter()
        let _ = (df.dateFormat = "M月d日 HH:mm")
        VStack(alignment: .leading, spacing: 6) {
            Button {
                withAnimation(.easeInOut(duration: 0.16)) { showPreviousDetails.toggle() }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "clock.arrow.circlepath")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("上次通话回顾 · \(df.string(from: record.date))")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.primary)
                    Spacer()
                    Image(systemName: showPreviousDetails ? "chevron.up" : "chevron.down")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Text(record.topicPreview)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(showPreviousDetails ? nil : 2)

            if showPreviousDetails {
                if let s = record.structuredSummary {
                    if !s.agreed.isEmpty {
                        Text("上次已定：" + s.agreed.joined(separator: "；"))
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    if !s.nextSteps.isEmpty {
                        Text("上次待跟进：" + s.nextSteps.joined(separator: "；"))
                            .font(.caption2)
                            .foregroundStyle(.orange)
                    }
                } else if let text = record.summary, !text.isEmpty {
                    Text(text)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color.primary.opacity(0.045))
        )
    }
}

/// Compact one-line banner shown before AI starts when you have talked to this customer before.
private struct PreviousCallBanner: View {
    let record: AICallRecord

    private static let df: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "M月d日"
        return f
    }()

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "clock.arrow.circlepath")
                .font(.caption)
                .foregroundStyle(Color.accentColor)
            VStack(alignment: .leading, spacing: 1) {
                Text("上次通话（\(Self.df.string(from: record.date))）")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.secondary)
                Text(record.topicPreview)
                    .font(.caption)
                    .lineLimit(2)
            }
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color.accentColor.opacity(0.08))
        )
    }
}

/// Reusable language selector shared between pre-call panel and live toolbar.
struct CallLanguagePickerView: View {
    @EnvironmentObject private var ai: AICallAssistant

    var body: some View {
        Picker("", selection: Binding(
            get: { ai.autoLanguage ? "auto" : ai.language.id },
            set: { value in
                if value == "auto" {
                    ai.autoLanguage = true
                } else {
                    ai.autoLanguage = false
                    ai.setTargetLanguage(CallLanguage.find(value))
                }
            }
        )) {
            ForEach(CallLanguage.all) { Text("客户说\($0.name)").tag($0.id) }
            Divider()
            Text(ai.autoLanguage ? "开头自动识别（\(ai.language.name)）" : "开头自动识别").tag("auto")
        }
        .labelsHidden()
        .frame(width: 155)
    }
}

// MARK: - Settings

struct AICallSettingsCard: View {
    @EnvironmentObject private var aiSettings: AICallSettings
    @State private var testResult: String?
    @State private var testing = false
    @State private var showingHistory = false
    @State private var showAdvancedModels = false

    private let voices = ["marin", "cedar", "alloy", "ash", "ballad", "coral", "echo", "sage", "shimmer", "verse"]
    private let geminiVoices = ["Kore", "Aoede", "Leda", "Zephyr", "Puck", "Charon", "Fenrir", "Orus"]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("打电话时让 AI 做同声传译（边说边翻），并自动把聊过的内容实时总结成中文谈话概要。")
                .font(.caption)
                .foregroundStyle(.secondary)

            row("AI 引擎") {
                Picker("", selection: $aiSettings.provider) {
                    Text("OpenAI（支持同传）").tag("openai")
                    Text("Gemini（逐句）").tag("gemini")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 260)
                Spacer()
            }

            if aiSettings.usesGemini {
                row("Gemini Key") {
                    SecureField("AIza…", text: $aiSettings.geminiKey)
                        .textFieldStyle(.roundedBorder)
                }
                row("实时语音模型") {
                    TextField("gemini-3.8-live", text: $aiSettings.geminiModel)
                        .textFieldStyle(.roundedBorder)
                }
                row("概要文字模型") {
                    TextField("gemini-flash-latest", text: $aiSettings.geminiTextModel)
                        .textFieldStyle(.roundedBorder)
                }
                row("AI 的声音") {
                    Picker("", selection: $aiSettings.geminiVoice) {
                        ForEach(geminiVoices, id: \.self) { Text($0).tag($0) }
                    }
                    .labelsHidden()
                    .frame(width: 140)
                    Spacer()
                }
            } else {
                row("API Key") {
                    SecureField("sk-…", text: $aiSettings.apiKey)
                        .textFieldStyle(.roundedBorder)
                }
                row("服务地址") {
                    TextField("https://api.openai.com", text: $aiSettings.baseURL)
                        .textFieldStyle(.roundedBorder)
                }
                row("翻译方式") {
                    Picker("", selection: $aiSettings.translateStyle) {
                        Text("同声传译（边说边翻 · 推荐）").tag("simultaneous")
                        Text("逐句翻译（说完再翻，整理语句）").tag("turn")
                    }
                    .labelsHidden()
                    .frame(width: 260)
                    Spacer()
                }
                if aiSettings.usesSimultaneous {
                    Text("同声传译使用 OpenAI 专用同传模型（gpt-realtime-translate），会自动贴近说话人自己的音色语气，并启用官方降噪与 24 kHz 高清麦克风输入。")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .padding(.leading, 102)
                }
                row("概要文字模型") {
                    TextField("gpt-5.4-mini", text: $aiSettings.textModel)
                        .textFieldStyle(.roundedBorder)
                }

                DisclosureGroup(
                    aiSettings.usesSimultaneous ? "逐句翻译 / AI 代打高级模型设置" : "语音模型与音色设置",
                    isExpanded: Binding(
                        get: { !aiSettings.usesSimultaneous || showAdvancedModels },
                        set: { showAdvancedModels = $0 }
                    )
                ) {
                    VStack(alignment: .leading, spacing: 8) {
                        row("实时语音模型") {
                            TextField("gpt-realtime-2.1", text: $aiSettings.realtimeModel)
                                .textFieldStyle(.roundedBorder)
                        }
                        row("AI 的声音") {
                            Picker("", selection: $aiSettings.voice) {
                                ForEach(voices, id: \.self) { Text($0).tag($0) }
                            }
                            .labelsHidden()
                            .frame(width: 140)
                            Spacer()
                        }
                    }
                    .padding(.top, 6)
                }
                .font(.caption)
            }

            Divider().padding(.vertical, 2)

            Toggle("我戴着耳机（全双工：边听中文边说话不闭麦，开启近场降噪）", isOn: $aiSettings.headsetMode)
                .font(.callout)

            row("客户原声音量") {
                Picker("", selection: $aiSettings.callerMonitorVolume) {
                    Text("静音 (0%)").tag(0.0)
                    Text("轻背景 (18% · 推荐)").tag(0.18)
                    Text("较清晰 (40%)").tag(0.40)
                }
                .labelsHidden()
                .frame(width: 180)
                Spacer()
            }

            Toggle("通话中自动滚动更新「实时谈话概要」（防忘掉前面聊的内容）", isOn: $aiSettings.autoLiveSummary)
                .font(.callout)

            Toggle("新号码开头自动识别客户语言（识别一次即锁定，不中途乱跳）", isOn: $aiSettings.autoDetectLanguage)
                .font(.callout)

            row("你的称呼") {
                TextField("AI 替你说话时怎么称呼你，例如 Wei", text: $aiSettings.ownerName)
                    .textFieldStyle(.roundedBorder)
            }
            Toggle("来电响几声后让 AI 自动接听，问清对方是谁、什么事", isOn: $aiSettings.autoScreenIncoming)

            HStack {
                Button(testing ? "检测中…" : "检测连接") {
                    testing = true
                    Task {
                        testResult = await AICallAssistant.testConnection()
                        testing = false
                    }
                }
                .disabled(testing)
                if let testResult {
                    Text(testResult).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button("AI 谈话记录与概要…") { showingHistory = true }
            }
            .sheet(isPresented: $showingHistory) { AICallHistorySheet() }

            Text("强烈建议商务同传时戴耳机并勾选「我戴着耳机」，客户原声不会回传，你可以随时自然插话。")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .padding(14)
    }

    private func row<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        HStack(spacing: 10) {
            Text(title)
                .frame(width: 92, alignment: .leading)
            content()
        }
    }
}
