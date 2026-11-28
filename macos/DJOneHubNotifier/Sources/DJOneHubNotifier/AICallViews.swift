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
            Text("拨打 \(number)。AI 会用对方的语言跟他聊，你在屏幕上看中文字幕，随时可以点“我来接”。")
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
                        Text("例如：问这家餐厅明天下午 3 点还能不能订 2 个人，能就用 Wei 的名字订，留这个号码。")
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
        .onAppear { languageID = CallLanguage.guess(for: number).id }
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

// MARK: - In-call panel

struct AICallPanel: View {
    @EnvironmentObject private var calls: CallCenter
    @EnvironmentObject private var ai: AICallAssistant
    @EnvironmentObject private var aiSettings: AICallSettings
    let connected: Bool
    @State private var askingGoal = false

    var body: some View {
        VStack(spacing: 10) {
            if !ai.captions.isEmpty || ai.summary != nil {
                captionList
            }
            if let error = ai.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
            }
            if ai.isOn {
                if ai.mode == .translate {
                    languagePicker
                }
                Text(ai.status)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                Button {
                    calls.aiTakeOver()
                } label: {
                    Label("我来接", systemImage: "person.wave.2.fill")
                        .frame(minWidth: 140)
                }
                .buttonStyle(.borderedProminent)
                .tint(PhoneStyle.green)
                .controlSize(.large)
            } else if connected {
                HStack(spacing: 10) {
                    languagePicker
                    Button {
                        calls.startAITranslate()
                    } label: {
                        Label("AI 翻译", systemImage: "character.bubble")
                    }
                    Button {
                        askingGoal = true
                    } label: {
                        Label("AI 替我说", systemImage: "sparkles")
                    }
                }
                .disabled(!aiSettings.isConfigured)
                if !aiSettings.isConfigured {
                    Text("在 设置 › AI 通话助手 填好 API Key 后可用")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .sheet(isPresented: $askingGoal) { AIGoalSheet() }
    }

    /// "自动识别" (default) or a fixed language for the other person.
    private var languagePicker: some View {
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
            Text(ai.autoLanguage ? "自动识别（\(ai.language.name)）" : "自动识别").tag("auto")
            Divider()
            ForEach(CallLanguage.all) { Text("对方说\($0.name)").tag($0.id) }
        }
        .labelsHidden()
        .frame(width: 170)
    }

    private var captionList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(ai.captions) { caption in
                        CaptionRow(caption: caption).id(caption.id)
                    }
                    if let summary = ai.summary {
                        VStack(alignment: .leading, spacing: 4) {
                            Label("通话总结", systemImage: "list.bullet.rectangle")
                                .font(.caption.weight(.semibold))
                            Text(summary).font(.callout).textSelection(.enabled)
                        }
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(RoundedRectangle(cornerRadius: 10).fill(Color.accentColor.opacity(0.12)))
                        .id("summary")
                    }
                }
                .padding(10)
            }
            .frame(maxWidth: 520, maxHeight: 260)
            .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.05)))
            .onChange(of: ai.captions.count) { _ in
                if let last = ai.captions.last { withAnimation { proxy.scrollTo(last.id, anchor: .bottom) } }
            }
        }
    }
}

private struct CaptionRow: View {
    let caption: AICaption

    private var who: (String, Color) {
        switch caption.speaker {
        case .them: return ("对方", .blue)
        case .me: return ("你", PhoneStyle.green)
        case .ai: return ("AI", .purple)
        case .note: return ("", .secondary)
        }
    }

    var body: some View {
        if caption.speaker == .note {
            Text(caption.text)
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
        } else {
            HStack(alignment: .top, spacing: 8) {
                Text(who.0)
                    .font(.caption.weight(.bold))
                    .foregroundStyle(who.1)
                    .frame(width: 30, alignment: .leading)
                VStack(alignment: .leading, spacing: 2) {
                    if let zh = caption.chinese, !zh.isEmpty {
                        Text(zh).font(.callout)
                        if !caption.text.isEmpty {
                            Text(caption.text).font(.caption).foregroundStyle(.secondary)
                        }
                    } else {
                        Text(caption.text).font(.callout)
                    }
                }
                .textSelection(.enabled)
            }
        }
    }
}

// MARK: - Settings

struct AICallSettingsCard: View {
    @EnvironmentObject private var aiSettings: AICallSettings
    @State private var testResult: String?
    @State private var testing = false

    private let voices = ["marin", "cedar", "alloy", "ash", "ballad", "coral", "echo", "sage", "shimmer", "verse"]
    private let geminiVoices = ["Kore", "Aoede", "Leda", "Zephyr", "Puck", "Charon", "Fenrir", "Orus"]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("打电话时让 AI 当翻译，或者直接替你说话；来电也能让 AI 先接、问清是谁。只在声音通道正常的模块上能用。")
                .font(.caption)
                .foregroundStyle(.secondary)
            row("AI 引擎") {
                Picker("", selection: $aiSettings.provider) {
                    Text("OpenAI").tag("openai")
                    Text("Gemini").tag("gemini")
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 200)
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
                row("文字模型") {
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
                row("实时语音模型") {
                    TextField("gpt-realtime-2.1", text: $aiSettings.realtimeModel)
                        .textFieldStyle(.roundedBorder)
                }
                row("文字模型") {
                    TextField("gpt-5.4-mini", text: $aiSettings.textModel)
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
            }
            Text("翻译模式建议戴耳机，免得 Mac 外放的中文被麦克风收进去。")
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
