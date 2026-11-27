import Foundation
import Security
import SwiftUI

// MARK: - Settings

/// Where the call AI lives and how it sounds. The key sits in the Keychain.
@MainActor
final class AICallSettings: ObservableObject {
    static let shared = AICallSettings()

    @Published var apiKey: String { didSet { Keychain.set(apiKey, for: "openai-api-key") } }
    @AppStorage("ai.baseURL") var baseURL = "https://api.openai.com"
    @AppStorage("ai.realtimeModel") var realtimeModel = "gpt-realtime-2.1"
    @AppStorage("ai.textModel") var textModel = "gpt-5.4-mini"
    @AppStorage("ai.voice") var voice = "marin"
    @AppStorage("ai.ownerName") var ownerName = ""
    @AppStorage("ai.autoScreen") var autoScreenIncoming = false

    var isConfigured: Bool { !apiKey.trimmingCharacters(in: .whitespaces).isEmpty }

    private init() {
        apiKey = Keychain.get("openai-api-key") ?? ""
    }

    var trimmedBase: String {
        var base = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        while base.hasSuffix("/") { base.removeLast() }
        if base.hasSuffix("/v1") { base.removeLast(3) }
        // The OpenAI website address is easy to paste by mistake.
        base = base.replacingOccurrences(of: "platform.openai.com", with: "api.openai.com")
        return base.isEmpty ? "https://api.openai.com" : base
    }
}

enum Keychain {
    private static let service = "com.jamie.djonehub.notifier.ai"

    static func get(_ account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func set(_ value: String, for account: String) {
        let base: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(base as CFDictionary)
        guard !value.isEmpty else { return }
        var add = base
        add[kSecValueData as String] = Data(value.utf8)
        SecItemAdd(add as CFDictionary, nil)
    }
}

// MARK: - Languages

struct CallLanguage: Identifiable, Hashable {
    let id: String      // English name, used in prompts
    let name: String    // shown to the user

    static let all: [CallLanguage] = [
        ("English", "英语"), ("Japanese", "日语"), ("Korean", "韩语"), ("German", "德语"),
        ("French", "法语"), ("Spanish", "西班牙语"), ("Italian", "意大利语"), ("Portuguese", "葡萄牙语"),
        ("Russian", "俄语"), ("Dutch", "荷兰语"), ("Polish", "波兰语"), ("Turkish", "土耳其语"),
        ("Arabic", "阿拉伯语"), ("Hebrew", "希伯来语"), ("Thai", "泰语"), ("Vietnamese", "越南语"),
        ("Indonesian", "印尼语"), ("Malay", "马来语"), ("Hindi", "印地语"), ("Swedish", "瑞典语"),
        ("Norwegian", "挪威语"), ("Danish", "丹麦语"), ("Finnish", "芬兰语"), ("Greek", "希腊语"),
        ("Czech", "捷克语"), ("Hungarian", "匈牙利语"), ("Romanian", "罗马尼亚语"), ("Ukrainian", "乌克兰语"),
        ("Cantonese", "粤语"), ("Mandarin Chinese", "普通话"),
    ].map { CallLanguage(id: $0.0, name: $0.1) }

    static func find(_ id: String) -> CallLanguage { all.first { $0.id == id } ?? all[0] }

    private static let byCountry: [String: String] = [
        "US": "English", "CA": "English", "GB": "English", "IE": "English", "AU": "English", "NZ": "English",
        "SG": "English", "PH": "English", "IN": "English", "PK": "English", "NG": "English", "KE": "English",
        "ZA": "English", "DE": "German", "AT": "German", "CH": "German", "FR": "French", "BE": "French",
        "MA": "French", "IT": "Italian", "ES": "Spanish", "MX": "Spanish", "AR": "Spanish", "CL": "Spanish",
        "CO": "Spanish", "PE": "Spanish", "PT": "Portuguese", "BR": "Portuguese", "NL": "Dutch", "PL": "Polish",
        "TR": "Turkish", "RU": "Russian", "KZ": "Russian", "UA": "Ukrainian", "JP": "Japanese", "KR": "Korean",
        "TH": "Thai", "VN": "Vietnamese", "ID": "Indonesian", "MY": "Malay", "AE": "Arabic", "SA": "Arabic",
        "QA": "Arabic", "KW": "Arabic", "EG": "Arabic", "IL": "Hebrew", "SE": "Swedish", "NO": "Norwegian",
        "DK": "Danish", "FI": "Finnish", "GR": "Greek", "CZ": "Czech", "HU": "Hungarian", "RO": "Romanian",
        "HK": "Cantonese", "MO": "Cantonese", "TW": "Mandarin Chinese", "CN": "Mandarin Chinese",
    ]

    /// A likely language for a phone number, from its country code.
    static func guess(for number: String?) -> CallLanguage {
        var digits = (number ?? "").filter { $0.isNumber || $0 == "+" }
        if digits.hasPrefix("00") { digits = "+" + digits.dropFirst(2) }
        guard digits.hasPrefix("+") else { return find("English") }
        let body = digits.dropFirst()
        let match = DialCountry.all
            .filter { body.hasPrefix($0.code) }
            .max { $0.code.count < $1.code.count }
        return find(match.flatMap { byCountry[$0.iso] } ?? "English")
    }
}

// MARK: - Audio helpers

enum PCM {
    /// 8 kHz -> 24 kHz, linear interpolation.
    static func up3(_ data: Data) -> Data {
        let n = data.count / 2
        guard n > 0 else { return Data() }
        var out = [Int16](repeating: 0, count: n * 3)
        data.withUnsafeBytes { raw in
            let s = raw.bindMemory(to: Int16.self)
            for i in 0 ..< n {
                let a = Int(Int16(littleEndian: s[i]))
                let b = i + 1 < n ? Int(Int16(littleEndian: s[i + 1])) : a
                out[i * 3] = Int16(a)
                out[i * 3 + 1] = Int16((a * 2 + b) / 3)
                out[i * 3 + 2] = Int16((a + b * 2) / 3)
            }
        }
        return out.withUnsafeBytes { Data($0) }
    }

    /// 24 kHz -> 8 kHz, averaging each group of three.
    static func down3(_ data: Data) -> Data {
        let n = data.count / 2 / 3
        guard n > 0 else { return Data() }
        var out = [Int16](repeating: 0, count: n)
        data.withUnsafeBytes { raw in
            let s = raw.bindMemory(to: Int16.self)
            for i in 0 ..< n {
                let sum = Int(Int16(littleEndian: s[i * 3])) + Int(Int16(littleEndian: s[i * 3 + 1]))
                    + Int(Int16(littleEndian: s[i * 3 + 2]))
                out[i] = Int16(sum / 3)
            }
        }
        return out.withUnsafeBytes { Data($0) }
    }
}

// MARK: - Realtime session

/// One OpenAI Realtime speech-to-speech conversation over a WebSocket.
final class RealtimeVoiceSession: NSObject, @unchecked Sendable {
    var onAudio: ((Data) -> Void)?              // 24 kHz PCM16
    var onHeard: ((String) -> Void)?            // transcript of what came in
    var onSaid: ((String) -> Void)?             // transcript of what the AI said
    var onSpeechStarted: (() -> Void)?
    var onError: ((String) -> Void)?

    private var task: URLSessionWebSocketTask?
    private let queue = DispatchQueue(label: "djonehub.ai.realtime")
    private var pending = Data()
    private var closed = false
    private var leftover = Data()   // odd byte carry for 24 kHz output
    private var baseInstructions = ""
    // Nothing is heard or said until the server confirms our instructions;
    // before that it answers with its own generic assistant persona.
    private var sessionConfig: [String: Any] = [:]
    private var updateSent = false
    private var ready = false
    private var queuedResponse: [String: Any]?

    func connect(base: String, model: String, key: String, session: [String: Any]) {
        let wsBase = base.replacingOccurrences(of: "https://", with: "wss://")
            .replacingOccurrences(of: "http://", with: "ws://")
        guard let url = URL(string: "\(wsBase)/v1/realtime?model=\(model)") else {
            onError?("AI 地址无效")
            return
        }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        let task = URLSession.shared.webSocketTask(with: request)
        self.task = task
        task.resume()
        baseInstructions = session["instructions"] as? String ?? ""
        sessionConfig = session
        receive()
        // Some relays skip session.created/updated: don't wait forever.
        queue.asyncAfter(deadline: .now() + 3) { [weak self] in
            guard let self, !self.closed, !self.updateSent else { return }
            self.updateSent = true
            self.send(["type": "session.update", "session": self.sessionConfig])
        }
        queue.asyncAfter(deadline: .now() + 6) { [weak self] in
            guard let self, !self.closed, !self.ready else { return }
            AICallLog.write("no session.updated after 6s, going ahead")
            self.ready = true
            if let r = self.queuedResponse { self.queuedResponse = nil; self.send(r) }
        }
    }

    /// Adds 8 kHz call/mic audio; sent in ~100 ms batches.
    func appendAudio8k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending.append(pcm)
            guard self.pending.count >= 1_600 else { return }
            let chunk = PCM.up3(self.pending)
            self.pending.removeAll(keepingCapacity: true)
            self.send(["type": "input_audio_buffer.append", "audio": chunk.base64EncodedString()])
        }
    }

    /// Asks the AI to speak now. `extra` is added to (not instead of) the
    /// session's instructions, so the goal is never lost.
    func requestResponse(_ extra: String? = nil) {
        var response: [String: Any] = [:]
        if let extra { response["instructions"] = baseInstructions + "\n\nRight now: " + extra }
        let event: [String: Any] = ["type": "response.create", "response": response]
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            if self.ready { self.send(event) } else { self.queuedResponse = event }
        }
    }

    func close() {
        queue.async { [weak self] in
            guard let self else { return }
            self.closed = true
            self.task?.cancel(with: .normalClosure, reason: nil)
            self.task = nil
        }
    }

    private func send(_ event: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: event),
              let text = String(data: data, encoding: .utf8) else { return }
        task?.send(.string(text)) { [weak self] error in
            if let error, let self, !self.closed {
                self.onError?("AI 连接断开：\(error.localizedDescription)")
            }
        }
    }

    private func receive() {
        task?.receive { [weak self] result in
            guard let self, !self.closed else { return }
            switch result {
            case .failure(let error):
                self.closed = true
                self.onError?("AI 连接失败：\(error.localizedDescription)")
            case .success(let message):
                if case .string(let text) = message { self.handle(text) }
                if case .data(let data) = message, let text = String(data: data, encoding: .utf8) { self.handle(text) }
                self.receive()
            }
        }
    }

    private func handle(_ text: String) {
        guard let data = text.data(using: .utf8),
              let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = event["type"] as? String else { return }
        AICallLog.write(type == "error" ? "error \(text.prefix(300))" : type)
        switch type {
        case "session.created":
            queue.async { [weak self] in
                guard let self, !self.updateSent else { return }
                self.updateSent = true
                self.send(["type": "session.update", "session": self.sessionConfig])
            }
        case "session.updated":
            queue.async { [weak self] in
                guard let self, !self.ready else { return }
                self.ready = true
                if let r = self.queuedResponse { self.queuedResponse = nil; self.send(r) }
            }
        case "response.output_audio.delta", "response.audio.delta":
            if let b64 = event["delta"] as? String, var pcm = Data(base64Encoded: b64) {
                if !leftover.isEmpty { pcm = leftover + pcm; leftover.removeAll() }
                if pcm.count % 2 == 1 { leftover = pcm.suffix(1); pcm.removeLast() }
                onAudio?(pcm)
            }
        case "response.output_audio_transcript.done", "response.audio_transcript.done":
            if let t = (event["transcript"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty {
                onSaid?(t)
            }
        case "conversation.item.input_audio_transcription.completed":
            if let t = (event["transcript"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty {
                onHeard?(t)
            }
        case "input_audio_buffer.speech_started":
            onSpeechStarted?()
        case "error":
            let err = event["error"] as? [String: Any]
            let msg = (err?["message"] as? String) ?? "未知错误"
            // Cancelling a response that already finished is harmless.
            if (err?["code"] as? String) != "response_cancel_not_active" {
                onError?("AI 出错：\(msg)")
            }
        default:
            break
        }
    }

    /// Session settings in the Realtime API's GA shape.
    static func config(
        instructions: String,
        voice: String,
        interrupt: Bool,
        transcribe: Bool = true
    ) -> [String: Any] {
        var input: [String: Any] = [
            "format": ["type": "audio/pcm", "rate": 24_000],
            "turn_detection": [
                "type": "server_vad",
                "silence_duration_ms": 600,
                "create_response": true,
                "interrupt_response": interrupt,
            ],
        ]
        if transcribe { input["transcription"] = ["model": "gpt-4o-mini-transcribe"] }
        return [
            "type": "realtime",
            "instructions": instructions,
            "output_modalities": ["audio"],
            "audio": [
                "input": input,
                "output": ["format": ["type": "audio/pcm", "rate": 24_000], "voice": voice],
            ],
        ]
    }
}

// MARK: - Assistant

/// What is on the call screen while the AI helps.
struct AICaption: Identifiable, Equatable {
    enum Speaker { case them, me, ai, note }
    let id = UUID()
    let speaker: Speaker
    var text: String
    var chinese: String?
}

@MainActor
final class AICallAssistant: ObservableObject {
    enum Mode: Equatable { case off, translate, agent }

    @Published private(set) var mode: Mode = .off
    @Published private(set) var captions: [AICaption] = []
    @Published var language: CallLanguage = CallLanguage.find("English")
    @Published private(set) var status = ""
    @Published private(set) var summary: String?
    @Published private(set) var lastError: String?

    /// Goal set before dialing; the agent starts once the call connects.
    var pendingGoal: String?
    var pendingLanguage: CallLanguage?

    private var sessions: [RealtimeVoiceSession] = []
    private weak var audio: VoiceAudioService?
    private var callID: String?
    private var goal = ""
    private let settings = AICallSettings.shared

    var isOn: Bool { mode != .off }

    // MARK: start

    func startTranslate(callID: String, number: String?, audio: VoiceAudioService) {
        guard prepare(callID: callID, number: number, audio: audio) else { return }
        let lang = language.id
        let inbound = RealtimeVoiceSession()
        let outbound = RealtimeVoiceSession()
        let base = """
        You are a live phone interpreter. You never take part in the conversation, \
        never answer questions, never add comments or greetings. You only repeat \
        what you just heard, translated, in a natural spoken style, keeping names, \
        numbers, dates and addresses exact.
        """
        // While the Chinese translation plays on the speaker, the microphone
        // would hear it; hold the mic back so it isn't translated again.
        let gate = SpeakerGate()
        inbound.onAudio = { [weak audio] pcm in
            gate.extend(by: Double(pcm.count / 2) / 24_000)
            audio?.playAssistant(pcm)
        }
        inbound.onHeard = { [weak self] t in Task { @MainActor in self?.add(.them, t) } }
        inbound.onSaid = { [weak self] t in Task { @MainActor in self?.attachChinese(t, to: .them) } }
        outbound.onAudio = { [weak audio] pcm in audio?.sendAssistantSpeech(PCM.down3(pcm)) }
        outbound.onHeard = { [weak self] t in Task { @MainActor in self?.add(.me, t) } }
        outbound.onSaid = { [weak self] t in Task { @MainActor in self?.add(.ai, t) } }
        for s in [inbound, outbound] {
            s.onError = { [weak self] e in Task { @MainActor in self?.fail(e) } }
        }
        audio.onRemoteAudio = { [weak inbound] pcm in inbound?.appendAudio8k(pcm) }
        audio.onLocalAudio = { [weak outbound] pcm in
            if !gate.isSpeaking { outbound?.appendAudio8k(pcm) }
        }
        connect(inbound, instructions: base + "\nTranslate everything you hear into Simplified Chinese (Mandarin).", interrupt: false)
        connect(outbound, instructions: base + "\nThe speaker talks Chinese. Translate everything into \(lang).", interrupt: false)
        sessions = [inbound, outbound]
        audio.setAIRouting(.translate)
        mode = .translate
        status = "AI 翻译中：你说中文，对方听到\(language.name)；对方说的话会翻成中文给你听"
    }

    /// The AI speaks for you. `screening` is the incoming-call receptionist.
    func startAgent(
        callID: String,
        number: String?,
        goal: String,
        screening: Bool,
        audio: VoiceAudioService,
        speakFirst: Bool
    ) {
        guard prepare(callID: callID, number: number, audio: audio) else { return }
        self.goal = goal
        let owner = settings.ownerName.trimmingCharacters(in: .whitespaces)
        let ownerText = owner.isEmpty ? "the owner of this phone" : owner
        let task = screening
            ? """
              Someone is calling \(ownerText), who can't take the call right now. Answer politely \
              as \(ownerText)'s assistant. Find out who is calling, from which company if any, \
              and what it is about, plus any callback number or deadline. Keep it short and \
              friendly; when you have that, say you will pass the message on and say goodbye. \
              Never agree to payments, never share personal details, verification codes or \
              passwords.
              """
            : """
              You are on a phone call on behalf of \(ownerText). Their goal, written in Chinese:
              <<<\(goal)>>>
              Pursue that goal politely and efficiently. Use only facts from the goal; if you are \
              asked for something you don't know, say you will check and get back. Never invent \
              personal details, never agree to pay or sign up beyond the goal. When the goal is \
              done or clearly impossible, thank them and say goodbye.
              """
        let instructions = """
        \(task)
        Speak \(language.id) by default. If the other person speaks a different language, \
        switch to theirs. Speak like a natural, brief human caller: one or two sentences per \
        turn, then let them talk. If asked whether you are an AI, say you are \(ownerText)'s AI \
        assistant.
        """
        let s = RealtimeVoiceSession()
        s.onAudio = { [weak audio] pcm in
            audio?.sendAssistantSpeech(PCM.down3(pcm))
            audio?.playAssistant(pcm)
        }
        let heard = SpeakerGate()
        s.onSpeechStarted = { [weak audio] in
            heard.extend(by: 60)
            // They started talking: stop the AI mid-sentence, like a person would.
            audio?.clearAssistantUplink()
            audio?.stopAssistantPlayback()
        }
        s.onHeard = { [weak self] t in Task { @MainActor in self?.add(.them, t, translate: true) } }
        s.onSaid = { [weak self] t in Task { @MainActor in self?.add(.ai, t, translate: true) } }
        s.onError = { [weak self] e in Task { @MainActor in self?.fail(e) } }
        audio.onRemoteAudio = { [weak s] pcm in s?.appendAudio8k(pcm) }
        audio.onLocalAudio = nil
        connect(s, instructions: instructions, interrupt: true)
        sessions = [s]
        audio.setAIRouting(.agent)
        mode = .agent
        status = screening ? "AI 正在替你接听，问清对方是谁、什么事" : "AI 正在替你通话"
        if speakFirst {
            // Give them a moment to say "hello" first (that answer starts the
            // AI by itself); speak up only if the line stays quiet.
            let kick = screening
                ? "Greet the caller briefly in \(language.id) as the assistant and ask who is calling and why."
                : "The call just connected. Greet briefly in \(language.id) and state why you are calling."
            DispatchQueue.main.asyncAfter(deadline: .now() + 2.5) { [weak s] in
                guard let s, !heard.isSpeaking else { return }
                s.requestResponse(kick)
            }
        }
    }

    private func prepare(callID: String, number: String?, audio: VoiceAudioService) -> Bool {
        guard settings.isConfigured else {
            lastError = "先在 设置 › AI 通话助手 里填 API Key"
            return false
        }
        guard audio.isRunning else {
            lastError = "通话声音还没接通，稍等一下再开 AI"
            return false
        }
        stopSessions()
        if self.callID != callID {
            captions = []
            summary = nil
        }
        self.callID = callID
        self.audio = audio
        lastError = nil
        return true
    }

    private func connect(_ s: RealtimeVoiceSession, instructions: String, interrupt: Bool) {
        s.connect(
            base: settings.trimmedBase,
            model: settings.realtimeModel,
            key: settings.apiKey,
            session: RealtimeVoiceSession.config(
                instructions: instructions,
                voice: settings.voice,
                interrupt: interrupt
            )
        )
    }

    // MARK: stop

    /// "我来接": hand the call back to you.
    func takeOver() {
        let wasAgent = mode == .agent
        stopSessions()
        add(.note, "你接过了电话")
        if wasAgent { summarize() }
    }

    /// The call ended.
    func callEnded() {
        let wasAgent = mode == .agent
        stopSessions()
        if wasAgent, captions.contains(where: { $0.speaker == .them }) { summarize() }
        pendingGoal = nil
        pendingLanguage = nil
    }

    private func stopSessions() {
        sessions.forEach { $0.close() }
        sessions = []
        audio?.onRemoteAudio = nil
        audio?.onLocalAudio = nil
        audio?.setAIRouting(.off)
        if mode != .off { mode = .off }
        status = ""
    }

    private func fail(_ message: String) {
        guard mode != .off else { return }
        lastError = message
        stopSessions()
        add(.note, "AI 已停止，电话交回给你：\(message)")
    }

    // MARK: captions

    private func add(_ speaker: AICaption.Speaker, _ text: String, translate: Bool = false) {
        // The translation can arrive before the transcript of what was said.
        if speaker == .them, mode == .translate,
           let i = captions.lastIndex(where: { $0.speaker == .them }),
           captions[i].text.isEmpty {
            captions[i].text = text
            return
        }
        captions.append(AICaption(speaker: speaker, text: text))
        if captions.count > 200 { captions.removeFirst(captions.count - 200) }
        guard translate, !looksChinese(text) else { return }
        let id = captions.last!.id
        Task {
            if let zh = try? await chat("把下面这句电话里的话翻译成简体中文，只输出译文：\n\(text)") {
                if let i = captions.firstIndex(where: { $0.id == id }) { captions[i].chinese = zh }
            }
        }
    }

    private func attachChinese(_ zh: String, to speaker: AICaption.Speaker) {
        if let i = captions.lastIndex(where: { $0.speaker == speaker && $0.chinese == nil }) {
            captions[i].chinese = zh
        } else {
            captions.append(AICaption(speaker: speaker, text: "", chinese: zh))
        }
    }

    private func looksChinese(_ s: String) -> Bool {
        let han = s.unicodeScalars.filter { (0x4E00 ... 0x9FFF).contains($0.value) }.count
        return han * 3 >= s.count
    }

    private func summarize() {
        let transcript = captions.compactMap { c -> String? in
            switch c.speaker {
            case .them: return "对方：\(c.text)"
            case .ai: return "AI：\(c.text)"
            case .me: return "我：\(c.text)"
            case .note: return nil
            }
        }.joined(separator: "\n")
        guard !transcript.isEmpty else { return }
        let goalLine = goal.isEmpty ? "" : "我交代的事：\(goal)\n"
        Task {
            do {
                let text = try await chat("""
                下面是 AI 替我打/接的一通电话。\(goalLine)用简体中文简短总结：对方是谁、说了什么、结果如何、我接下来要做什么。用 3–5 条短句。

                \(transcript)
                """)
                summary = text
                SystemNotifications.shared?.post(title: "AI 通话总结", body: text)
            } catch {
                summary = nil
            }
        }
    }

    // MARK: text model

    private func chat(_ prompt: String) async throws -> String {
        guard let url = URL(string: settings.trimmedBase + "/v1/chat/completions") else {
            throw URLError(.badURL)
        }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 30
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue("Bearer \(settings.apiKey)", forHTTPHeaderField: "Authorization")
        request.httpBody = try JSONSerialization.data(withJSONObject: [
            "model": settings.textModel,
            "messages": [["role": "user", "content": prompt]],
        ])
        let (data, response) = try await URLSession.shared.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200,
              let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let choices = json["choices"] as? [[String: Any]],
              let message = choices.first?["message"] as? [String: Any],
              let content = message["content"] as? String else {
            throw URLError(.badServerResponse)
        }
        return content.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Opens a live voice session and asks for one spoken word, to prove the
    /// whole speech path (not just the key) works before a real call.
    static func realtimeVoiceCheck() async -> String {
        let s = AICallSettings.shared
        let session = RealtimeVoiceSession()
        return await withCheckedContinuation { (cont: CheckedContinuation<String, Never>) in
            let lock = NSLock()
            var done = false
            var bytes = 0
            func finish(_ text: String) {
                lock.lock(); defer { lock.unlock() }
                guard !done else { return }
                done = true
                session.close()
                cont.resume(returning: text)
            }
            session.onAudio = { pcm in
                lock.lock(); bytes += pcm.count; let enough = bytes >= 24_000; lock.unlock()
                if enough { finish("连接正常，实时语音也能说话，可以用") }
            }
            session.onSaid = { _ in
                lock.lock(); let got = bytes; lock.unlock()
                finish(got > 0 ? "连接正常，实时语音也能说话，可以用" : "实时语音连上了，但没收到声音")
            }
            session.onError = { e in finish("Key 正常，但实时语音不行：\(e)") }
            session.connect(
                base: s.trimmedBase,
                model: s.realtimeModel,
                key: s.apiKey,
                session: RealtimeVoiceSession.config(
                    instructions: "Reply with one short word.",
                    voice: s.voice,
                    interrupt: true,
                    transcribe: false
                )
            )
            session.requestResponse("Say: hello.")
            DispatchQueue.global().asyncAfter(deadline: .now() + 20) {
                finish("实时语音 20 秒没回应（网络慢或模型 \(s.realtimeModel) 不可用）")
            }
        }
    }

    /// Checks the key and address with a tiny request.
    static func testConnection() async -> String {
        let s = AICallSettings.shared
        guard s.isConfigured, let url = URL(string: s.trimmedBase + "/v1/models/\(s.realtimeModel)") else {
            return "还没填 API Key"
        }
        var request = URLRequest(url: url)
        request.timeoutInterval = 15
        request.setValue("Bearer \(s.apiKey)", forHTTPHeaderField: "Authorization")
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            let code = (response as? HTTPURLResponse)?.statusCode ?? 0
            switch code {
            case 200: return await realtimeVoiceCheck()
            case 401: return "API Key 不对"
            case 404: return "这个 Key 用不了实时语音模型 \(s.realtimeModel)"
            default: return "服务器返回 \(code)"
            }
        } catch {
            return "连不上：\(error.localizedDescription)（在国内可能需要代理或中转地址）"
        }
    }
}

/// Remembers until when AI speech is coming out of the speaker.
final class SpeakerGate: @unchecked Sendable {
    private let lock = NSLock()
    private var until = Date.distantPast

    func extend(by seconds: Double) {
        lock.lock(); defer { lock.unlock() }
        until = max(until, Date()).addingTimeInterval(seconds)
    }

    var isSpeaking: Bool {
        lock.lock(); defer { lock.unlock() }
        return Date() < until.addingTimeInterval(0.4)
    }
}

/// Event names (no audio, no keys) for diagnosing AI calls.
enum AICallLog {
    private static let queue = DispatchQueue(label: "djonehub.ai.log")
    private static let url: URL = {
        let dir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/DJOneHub", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.appendingPathComponent("ai-call.log")
    }()

    static func write(_ line: String) {
        guard !line.hasSuffix(".delta") else { return }
        let stamp = ISO8601DateFormatter().string(from: Date())
        queue.async {
            let data = Data("\(stamp) \(line)\n".utf8)
            if let h = try? FileHandle(forWritingTo: url) {
                h.seekToEndOfFile(); h.write(data); try? h.close()
            } else {
                try? data.write(to: url)
            }
        }
    }
}
