import Foundation
import Security
import SwiftUI

// MARK: - Settings

/// Where the call AI lives and how it sounds. The key sits in the Keychain.
@MainActor
final class AICallSettings: ObservableObject {
    static let shared = AICallSettings()

    @Published var apiKey: String { didSet { KeyFile.write(apiKey) } }
    @AppStorage("ai.baseURL") var baseURL = "https://api.openai.com"
    @AppStorage("ai.realtimeModel") var realtimeModel = "gpt-realtime-2.1"
    @AppStorage("ai.textModel") var textModel = "gpt-5.4-mini"
    @AppStorage("ai.voice") var voice = "marin"
    @AppStorage("ai.ownerName") var ownerName = ""
    @AppStorage("ai.autoScreen") var autoScreenIncoming = false
    @AppStorage("ai.waitTone") var waitTone = true
    /// "openai" or "gemini": which live voice engine the call AI uses.
    @Published var provider = UserDefaults.standard.string(forKey: "ai.provider") ?? "openai" {
        didSet { UserDefaults.standard.set(provider, forKey: "ai.provider") }
    }
    @Published var geminiKey: String { didSet { KeyFile.write(geminiKey, name: "ai-gemini.key") } }
    @AppStorage("ai.geminiModel") var geminiModel = "gemini-3.8-live"
    @AppStorage("ai.geminiTextModel") var geminiTextModel = "gemini-flash-latest"
    @AppStorage("ai.geminiVoice") var geminiVoice = "Kore"

    var usesGemini: Bool { provider == "gemini" }
    var isConfigured: Bool {
        let key = usesGemini ? geminiKey : apiKey
        return !key.trimmingCharacters(in: .whitespaces).isEmpty
    }

    private init() {
        geminiKey = KeyFile.read("ai-gemini.key") ?? ""
        // A private file, not the keychain: keychain items made by an app are
        // tied to its signature, so every update popped an "allow" dialog.
        if let saved = KeyFile.read() {
            apiKey = saved
        } else if let old = Keychain.get("openai-api-key"), !old.isEmpty {
            apiKey = old
            KeyFile.write(old)
            Keychain.set("", for: "openai-api-key")
        } else {
            apiKey = ""
        }
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

/// The AI key in Application Support, readable only by this user (0600).
enum KeyFile {
    private static func url(_ name: String) -> URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/DJOneHub/\(name)")
    }
    private static var url: URL { url("ai-call.key") }

    static func read(_ name: String) -> String? {
        guard let text = try? String(contentsOf: url(name), encoding: .utf8) else { return nil }
        let key = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return key.isEmpty ? nil : key
    }

    static func write(_ key: String, name: String) {
        let path = url(name)
        let trimmed = key.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { try? FileManager.default.removeItem(at: path); return }
        try? FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: path.path, contents: Data(trimmed.utf8),
                                       attributes: [.posixPermissions: 0o600])
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path.path)
    }

    static func read() -> String? {
        guard let text = try? String(contentsOf: url, encoding: .utf8) else { return nil }
        let key = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return key.isEmpty ? nil : key
    }

    static func write(_ key: String) {
        let trimmed = key.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            try? FileManager.default.removeItem(at: url)
            return
        }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: url.path, contents: Data(trimmed.utf8),
                                       attributes: [.posixPermissions: 0o600])
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
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

    /// Languages with their own writing system, recognised without a model.
    static func byScript(_ text: String) -> CallLanguage? {
        var counts: [String: Int] = [:]
        for u in text.unicodeScalars {
            let lang: String?
            switch u.value {
            case 0x3040 ... 0x30FF: lang = "Japanese"
            case 0xAC00 ... 0xD7AF, 0x1100 ... 0x11FF: lang = "Korean"
            case 0x0E00 ... 0x0E7F: lang = "Thai"
            case 0x0600 ... 0x06FF: lang = "Arabic"
            case 0x0590 ... 0x05FF: lang = "Hebrew"
            case 0x0370 ... 0x03FF: lang = "Greek"
            case 0x0900 ... 0x097F: lang = "Hindi"
            case 0x0400 ... 0x04FF: lang = "Cyrillic"
            case 0x4E00 ... 0x9FFF: lang = "Han"
            default: lang = nil
            }
            if let lang { counts[lang, default: 0] += 1 }
        }
        guard let top = counts.max(by: { $0.value < $1.value }), top.value >= 2 else { return nil }
        switch top.key {
        case "Han": return counts["Japanese"] != nil ? find("Japanese") : find("Mandarin Chinese")
        case "Cyrillic":
            return text.contains(where: { "іїєґІЇЄҐ".contains($0) }) ? find("Ukrainian") : find("Russian")
        default: return find(top.key)
        }
    }

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
    var onSpeechStopped: (() -> Void)?
    var tag = ""
    var onHeardDelta: ((String) -> Void)?
    var onSaidDelta: ((String) -> Void)?
    var onError: ((String) -> Void)?
    private var awaitingFirstAudio = false

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
    private var answerAfterCurrent = false
    /// Errors about a single turn that must not end the whole session.
    private static let harmlessErrors: Set<String> = [
        "response_cancel_not_active",
        "input_audio_buffer_commit_empty",
        "conversation_already_has_active_response",
    ]

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

    /// Replaces the session's instructions mid-call (e.g. a new target language).
    func updateInstructions(_ text: String) {
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            self.baseInstructions = text
            self.sessionConfig["instructions"] = text
            if self.updateSent {
                self.send(["type": "session.update",
                           "session": ["type": "realtime", "instructions": text] as [String: Any]])
            }
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
        if !type.hasSuffix(".delta") { AICallLog.write("\(tag) " + (type == "error" ? "error \(text.prefix(300))" : type)) }
        switch type {
        case "response.output_audio_transcript.delta", "response.audio_transcript.delta":
            if let d = event["delta"] as? String, !d.isEmpty { onSaidDelta?(d) }
        case "conversation.item.input_audio_transcription.delta":
            if let d = event["delta"] as? String, !d.isEmpty { onHeardDelta?(d) }
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
            if awaitingFirstAudio { awaitingFirstAudio = false; AICallLog.write("\(tag) first audio") }
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
        case "input_audio_buffer.speech_stopped":
            awaitingFirstAudio = true
            onSpeechStopped?()
        case "response.done":
            // A turn finished while the previous translation was still being
            // spoken: answer it now instead of dropping it.
            queue.async { [weak self] in
                guard let self, self.answerAfterCurrent, !self.closed else { return }
                self.answerAfterCurrent = false
                self.send(["type": "response.create", "response": [:] as [String: Any]])
            }
        case "error":
            let err = event["error"] as? [String: Any]
            let msg = (err?["message"] as? String) ?? "未知错误"
            let code = (err?["code"] as? String) ?? ""
            let message = msg.lowercased()
            if code == "conversation_already_has_active_response" || message.contains("active response in progress") {
                // The speaker started a new sentence before the last one was
                // translated: queue it, it's not a failure.
                queue.async { [weak self] in self?.answerAfterCurrent = true }
            } else if Self.harmlessErrors.contains(code) {
                break
            } else if queue.sync(execute: { ready }) {
                // Once the call is running, one bad turn shouldn't hang up on
                // the AI; it is logged and the conversation carries on.
                AICallLog.write("non-fatal error \(code): \(msg)")
            } else {
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
        transcribe: Bool = true,
        eagerness: String = "high"
    ) -> [String: Any] {
        var input: [String: Any] = [
            "format": ["type": "audio/pcm", "rate": 24_000],
            // The talking agent waits until the person has actually finished
            // their thought (semantic VAD), which feels far less robotic than
            // jumping in after a fixed silence. Interpreters keep fast server VAD.
            // Interpreters too: translating half a thought is what makes it
            // sound broken, so they wait for the speaker to finish (eagerness).
            "turn_detection": [
                "type": "semantic_vad",
                "eagerness": eagerness,
                "create_response": true,
                "interrupt_response": interrupt,
            ] as [String: Any],
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
    /// Still being spoken: the words appear as they are recognised.
    var live = false
}

@MainActor
final class AICallAssistant: ObservableObject {
    enum Mode: Equatable { case off, translate, agent }

    @Published private(set) var mode: Mode = .off
    @Published private(set) var captions: [AICaption] = []
    @Published var language: CallLanguage = CallLanguage.find("English")
    @Published private(set) var status = ""
    /// Whose turn it is in translate mode ("对方正在说…", "请说" …).
    @Published private(set) var phase = ""
    @Published private(set) var summary: String?
    @Published private(set) var lastError: String?

    /// Goal set before dialing; the agent starts once the call connects.
    var pendingGoal: String?
    var pendingLanguage: CallLanguage?

    private var sessions: [any VoiceAISession] = []
    private weak var audio: VoiceAudioService?
    private var callID: String?
    private var goal = ""
    private let settings = AICallSettings.shared

    var isOn: Bool { mode != .off }

    // MARK: start

    static let interpreterBase = """
    You are a skilled live phone interpreter between your client and the other person. \
    You never take part in the conversation yourself: never answer questions, never add \
    comments, greetings or "they said". You speak only the translation, in first person, \
    sounding like a real person on the phone — natural, warm, not robotic.
    """

    static let inboundPrompt = interpreterBase + """

        Translate what the other person says into natural spoken Simplified Chinese (Mandarin) \
        for your client. Convey the meaning faithfully and completely, in the way a Chinese \
        speaker would actually say it; skip their filler words and false starts.
        """

    /// Both interpreter connections, opened before you press "AI 翻译".
    private final class InterpreterPair {
        let inbound: any VoiceAISession
        let outbound: any VoiceAISession
        var language: String
        let callID: String
        var broken = false
        init(inbound: any VoiceAISession, outbound: any VoiceAISession, language: String, callID: String) {
            self.inbound = inbound; self.outbound = outbound; self.language = language; self.callID = callID
        }
        func close() { inbound.close(); outbound.close() }
    }

    private var warmInterpreter: InterpreterPair?

    private func makeInterpreter(language lang: String, callID: String) -> InterpreterPair {
        let pair = InterpreterPair(inbound: Self.makeSession(), outbound: Self.makeSession(),
                                   language: lang, callID: callID)
        for s in [pair.inbound, pair.outbound] {
            s.onError = { [weak pair] e in
                AICallLog.write("warm interpreter error: \(e)")
                pair?.broken = true
            }
        }
        // "high": answer as soon as a sentence is complete. Patient end-of-turn
        // detection held every translation back 3–5 s on a real call.
        pair.inbound.tag = "in"
        pair.outbound.tag = "out"
        connect(pair.inbound, instructions: Self.inboundPrompt, interrupt: false, eagerness: "high")
        connect(pair.outbound, instructions: Self.outboundPrompt(base: Self.interpreterBase, language: lang),
                interrupt: false, eagerness: "high")
        return pair
    }

    /// Opens the interpreter as soon as a call starts, so "AI 翻译" works at once.
    func prewarmInterpreter(callID: String, number: String?) {
        guard settings.isConfigured, mode == .off, prewarmed == nil, pendingGoal == nil,
              warmInterpreter?.callID != callID, !endedCalls.contains(callID) else { return }
        warmInterpreter?.close()
        var lang = CallLanguage.guess(for: number)
        if lang.id == "Mandarin Chinese" { lang = CallLanguage.find("English") }
        warmInterpreter = makeInterpreter(language: lang.id, callID: callID)
        AICallLog.write("timing: prewarming interpreter")
    }

    private func dropWarmInterpreter() {
        warmInterpreter?.close()
        warmInterpreter = nil
    }

    func startTranslate(callID: String, number: String?, audio: VoiceAudioService) {
        guard prepare(callID: callID, number: number, audio: audio) else { return }
        // Until they speak, a Chinese-looking number most likely means a
        // foreign caller on a roaming/virtual number: start from English.
        if autoLanguage, language.id == "Mandarin Chinese" { language = CallLanguage.find("English") }
        let lang = language.id
        let pair: InterpreterPair
        if let warm = warmInterpreter, warm.callID == callID, !warm.broken {
            pair = warm
            AICallLog.write("timing: using prewarmed interpreter")
            if warm.language != lang {
                warm.outbound.updateInstructions(Self.outboundPrompt(base: Self.interpreterBase, language: lang))
                warm.language = lang
            }
        } else {
            warmInterpreter?.close()
            pair = makeInterpreter(language: lang, callID: callID)
        }
        warmInterpreter = nil
        let inbound = pair.inbound
        let outbound = pair.outbound
        let tone = settings.waitTone
        // While the Chinese translation plays on the speaker, the microphone
        // would hear it; hold the mic back so it isn't translated again.
        let gate = SpeakerGate()
        inbound.onAudio = { [weak audio] pcm in audio?.playAssistant(pcm) }
        inbound.onSpeechStarted = { [weak self] in Task { @MainActor in self?.setPhase("对方正在说…") } }
        inbound.onHeardDelta = { [weak self] d in Task { @MainActor in self?.draft("in.heard", .them, d) } }
        inbound.onSaidDelta = { [weak self] d in Task { @MainActor in
            self?.setPhase("正在把对方的话翻成中文给你听")
            self?.draft("in.said", .them, chinese: true, d)
        } }
        inbound.onHeard = { [weak self] t in Task { @MainActor in
            self?.settle("in.heard", .them, t)
            self?.noticeLanguage(of: t)
        } }
        inbound.onSaid = { [weak self] t in Task { @MainActor in
            self?.settle("in.said", .them, chinese: true, t)
            self?.setPhase("轮到你说")
        } }
        let outDecimator = PhoneDecimator()
        outbound.onAudio = { [weak audio] pcm in
            audio?.setWaitingTone(false)
            audio?.sendAssistantSpeech(outDecimator.process(pcm))
        }
        outbound.onSpeechStarted = { [weak self] in
            Task { @MainActor in self?.setPhase("你在说…") }
        }
        // The pulse only fills the gap between you finishing and the
        // translation starting, and only if that gap is noticeable.
        outbound.onSpeechStopped = { [weak self, weak audio] in
            if tone { audio?.setWaitingTone(true) }
            Task { @MainActor in
                self?.setPhase("正在翻译给对方…")
                self?.capWaitingTone()
            }
        }
        outbound.onHeardDelta = { [weak self] d in Task { @MainActor in self?.draft("out.heard", .me, d) } }
        outbound.onSaidDelta = { [weak self] d in Task { @MainActor in
            self?.setPhase("正在把你的话说给对方听")
            self?.draft("out.said", .ai, d)
        } }
        outbound.onHeard = { [weak self] t in Task { @MainActor in self?.settle("out.heard", .me, t) } }
        outbound.onSaid = { [weak self, weak audio] t in
            audio?.setWaitingTone(false)
            Task { @MainActor in
                self?.settle("out.said", .ai, t)
                self?.setPhase("等对方回话")
            }
        }
        for s in [inbound, outbound] {
            s.onError = { [weak self] e in Task { @MainActor in self?.fail(e) } }
        }
        audio.onRemoteAudio = { [weak inbound] pcm in inbound?.appendAudio8k(pcm) }
        audio.onLocalAudio = { [weak outbound, weak audio] pcm in
            if audio?.isAssistantSounding == true { gate.hold() }
            if !gate.isSpeaking { outbound?.appendAudio8k(pcm) }
        }
        audio.onAssistantBacklogDropped = { [weak self] in Task { @MainActor in
            self?.add(.note, "对方说得太长，中文语音落后太多，跳过了一段；请看上面的字幕")
        } }
        interpreterBase = Self.interpreterBase
        sessions = [inbound, outbound]
        outboundSession = outbound
        audio.setAIRouting(.translate)
        mode = .translate
        usedModes.insert("AI 翻译")
        updateTranslateStatus()
        setPhase("请说，或等对方说")
    }

    private var toneCap: Task<Void, Never>?

    /// The pulse never runs on for long if no translation comes out.
    private func capWaitingTone() {
        toneCap?.cancel()
        toneCap = Task { [weak self] in
            try? await Task.sleep(for: .seconds(6))
            guard !Task.isCancelled else { return }
            self?.audio?.setWaitingTone(false)
        }
    }

    private func setPhase(_ text: String) {
        guard mode == .translate, phase != text else { return }
        phase = text
    }

    // MARK: language detection

    /// "自动识别": the other person's language is worked out from what they
    /// say, and your Chinese is translated into it.
    @Published var autoLanguage = true
    private var interpreterBase = ""
    private weak var outboundSession: (any VoiceAISession)?
    private var detecting = false

    private func updateTranslateStatus() {
        let who = autoLanguage ? "对方的语言（现在：\(language.name)，自动识别）" : language.name
        status = "AI 翻译中：你说中文，对方听到\(who)；对方说的话会翻成中文给你听"
    }

    /// Switches the interpreter to another target language mid-call.
    func setTargetLanguage(_ lang: CallLanguage, auto: Bool = false) {
        if auto && lang == language { return }
        language = lang
        if mode == .translate, let out = outboundSession {
            out.updateInstructions(Self.outboundPrompt(base: interpreterBase, language: lang.id))
            if auto { add(.note, "识别到对方说\(lang.name)，你说的中文会翻成\(lang.name)") }
            updateTranslateStatus()
        }
    }

    fileprivate func noticeLanguage(of text: String) {
        guard autoLanguage, mode == .translate, !detecting else { return }
        // Chinese on their side is nearly always your own voice coming back
        // down the line (or a Chinese speaker who doesn't need translating):
        // never switch the interpreter to Chinese-to-Chinese.
        if let lang = CallLanguage.byScript(text) {
            if lang.id != "Mandarin Chinese" { setTargetLanguage(lang, auto: true) }
            return
        }
        if looksChinese(text) { return }
        // Latin-script languages need a quick look from the text model.
        let words = text.split(separator: " ").count
        guard words >= 2 || text.count >= 8 else { return }
        detecting = true
        Task {
            defer { detecting = false }
            let names = CallLanguage.all.map(\.id).joined(separator: ", ")
            guard let answer = try? await chat("Which language is this phone-call sentence in? Answer with exactly one name from this list and nothing else: \(names).\n\n\(text)") else { return }
            let name = answer.trimmingCharacters(in: .whitespacesAndNewlines.union(.punctuationCharacters))
            if let lang = CallLanguage.all.first(where: { $0.id.caseInsensitiveCompare(name) == .orderedSame }),
               !lang.id.contains("Chinese") {
                setTargetLanguage(lang, auto: true)
            }
        }
    }

    static func outboundPrompt(base: String, language lang: String) -> String {
        base + """

        Your client speaks Chinese, often thinking aloud: pauses, "这个", "那个", "就是", "嗯", \
        repeating themselves, changing their mind mid-sentence. Work out what they actually \
        mean, then say it in clear, natural, fluent \(lang) the way a native speaker would \
        put it — complete and polite, not word for word. Drop fillers, hesitations and \
        repetitions; when they correct themselves, use only the corrected version. Keep every \
        fact exact (names, numbers, dates, times, addresses, prices). Don't add information \
        they didn't give, and don't answer for them. If what they said is too unclear to \
        translate, say nothing rather than guess. Always speak \(lang), whatever language \
        earlier turns were in.
        """
    }

    /// The AI speaks for you. `screening` is the incoming-call receptionist.
    func startAgent(
        callID: String,
        number: String?,
        goal: String,
        screening: Bool,
        audio: VoiceAudioService,
        speakFirst: Bool,
        lateStart: Bool = false
    ) {
        guard prepare(callID: callID, number: number, audio: audio) else { return }
        self.goal = goal
        dropWarmInterpreter()
        usedModes.insert(screening ? "AI 接听" : "AI 代打")
        let agent: AgentSession
        if let warm = prewarmed, warm.goal == goal, warm.screening == screening {
            agent = warm
            AICallLog.write("timing: using prewarmed AI session")
        } else {
            prewarmed?.session.close()
            agent = makeAgent(goal: goal, screening: screening, audio: audio)
        }
        prewarmed = nil
        let s = agent.session
        audio.onRemoteAudio = { [weak s] pcm in s?.appendAudio8k(pcm) }
        audio.onLocalAudio = nil
        sessions = [s]
        audio.setAIRouting(.agent)
        mode = .agent
        status = screening ? "AI 正在替你接听，问清对方是谁、什么事" : "AI 正在替你通话"
        if speakFirst {
            // Give them a moment to say "hello" first (that answer starts the
            // AI by itself); speak up only if the line stays quiet. When the
            // audio came up late they have most likely said hello already.
            let kick = screening
                ? "Pick up like a person would: a quick casual hello, say \(agent.ownerText) can't come to the phone, and ask who's calling."
                : lateStart
                    ? "The call connected a few seconds ago and they already said hello. Jump in naturally, in one breath: a quick casual hi, then why you're calling."
                    : "The call just connected and they're silent. Open like a real person would, in one breath: a quick casual hi, then why you're calling."
            let heard = agent.heard
            DispatchQueue.main.asyncAfter(deadline: .now() + (lateStart ? 0.3 : 2.0)) { [weak s] in
                guard let s, !heard.isSpeaking else { return }
                s.requestResponse(kick)
            }
        }
    }

    /// Connects the agent while the phone is still ringing, so it is ready
    /// the moment they pick up.
    func prewarmAgent(goal: String, language: CallLanguage, audio: VoiceAudioService) {
        guard settings.isConfigured, prewarmed == nil else { return }
        self.language = language
        prewarmed = makeAgent(goal: goal, screening: false, audio: audio)
        AICallLog.write("timing: prewarming AI while ringing")
    }

    func dropPrewarmed() {
        prewarmed?.session.close()
        prewarmed = nil
    }

    var hasPrewarmed: Bool { prewarmed != nil }

    private struct AgentSession {
        let session: any VoiceAISession
        let heard: SpeakerGate
        let goal: String
        let screening: Bool
        let ownerText: String
    }

    private var prewarmed: AgentSession?

    private func makeAgent(goal: String, screening: Bool, audio: VoiceAudioService) -> AgentSession {
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

        # Language
        Speak \(language.id) by default. If the other person speaks a different language, switch \
        to theirs and stay there.

        # How you sound
        You are on a real phone call. Sound like a relaxed, friendly person, not a customer-service \
        bot or a voice assistant.
        - Talk casually, with contractions and everyday words. Short sentences. Usually one thought \
        per turn, then stop and let them talk.
        - React before you answer, like people do: "oh, okay", "ah, got it", "mm, right", "oh nice". \
        In Chinese: "嗯", "哦这样啊", "好的好的", "对对". Use them naturally, not every time.
        - A small filler or self-correction now and then is fine ("um", "so, uh", "I mean"); never \
        overdo it.
        - Never recap or repeat back everything they said. Never say "I've noted that", "Thank you \
        for your help", "Is there anything else I can help you with", "Certainly", "Absolutely", \
        or "Great question". Don't over-thank and don't over-apologize.
        - No lists, no formal summaries, no reading out structure. Just talk.
        - Match their energy and pace. If they're brief, be brief. If they joke, it's fine to smile \
        in your voice.
        - Warm, natural intonation; speak at a normal conversational speed, a little faster than a \
        news reader.
        - If you didn't catch something, just ask like a person: "sorry, what was that?"
        - End calls the way people do: a quick "okay, thanks, bye!" — not a speech.

        Don't volunteer that you're an AI. If they ask directly, be honest: you're \(ownerText)'s \
        AI assistant, then carry on naturally.
        """
        let s = Self.makeSession()
        s.tag = "agent"
        let decimator = PhoneDecimator()
        s.onAudio = { [weak audio] pcm in
            audio?.sendAssistantSpeech(decimator.process(pcm))
            audio?.playAssistant(pcm)
        }
        let heard = SpeakerGate()
        s.onSpeechStarted = { [weak audio, weak self] in
            Task { @MainActor in self?.closeDraft("ag.said") }
            heard.extend(by: 60)
            // They started talking: stop the AI mid-sentence, like a person would.
            audio?.clearAssistantUplink()
            audio?.stopAssistantPlayback()
        }
        s.onHeardDelta = { [weak self] d in Task { @MainActor in self?.draft("ag.heard", .them, d) } }
        s.onSaidDelta = { [weak self] d in Task { @MainActor in self?.draft("ag.said", .ai, d) } }
        s.onHeard = { [weak self] t in Task { @MainActor in self?.settle("ag.heard", .them, t, translate: true) } }
        s.onSaid = { [weak self] t in Task { @MainActor in self?.settle("ag.said", .ai, t, translate: true) } }
        s.onError = { [weak self] e in Task { @MainActor in self?.fail(e) } }
        connect(s, instructions: instructions, interrupt: true)
        return AgentSession(session: s, heard: heard, goal: goal, screening: screening, ownerText: ownerText)
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
            drafts = [:]
            summary = nil
            autoLanguage = true
            usedModes = []
            callStartedAt = Date()
        }
        callNumber = number
        self.callID = callID
        self.audio = audio
        lastError = nil
        return true
    }

    static func makeSession() -> any VoiceAISession {
        AICallSettings.shared.usesGemini ? GeminiLiveSession() : RealtimeVoiceSession()
    }

    private func connect(_ s: any VoiceAISession, instructions: String, interrupt: Bool,
                         eagerness: String = "high") {
        AICallLog.write("timing: AI connecting (\(settings.provider))")
        Self.connect(s, instructions: instructions, interrupt: interrupt, eagerness: eagerness)
    }

    static func connect(_ s: any VoiceAISession, instructions: String, interrupt: Bool,
                        eagerness: String = "high", transcribe: Bool = true) {
        let settings = AICallSettings.shared
        if let g = s as? GeminiLiveSession {
            g.connect(key: settings.geminiKey, model: settings.geminiModel, voice: settings.geminiVoice,
                      instructions: instructions, interrupt: interrupt, eagerness: eagerness)
        } else if let r = s as? RealtimeVoiceSession {
            r.connect(
                base: settings.trimmedBase,
                model: settings.realtimeModel,
                key: settings.apiKey,
                session: RealtimeVoiceSession.config(
                    instructions: instructions,
                    voice: settings.voice,
                    interrupt: interrupt,
                    transcribe: transcribe,
                    eagerness: eagerness
                )
            )
        }
    }


    // MARK: stop

    /// "我来接": hand the call back to you.
    func takeOver() {
        let wasAgent = mode == .agent
        stopSessions()
        add(.note, "你接过了电话")
        saveRecord(summarizing: wasAgent)
    }

    /// The call ended.
    func callEnded() {
        if let warm = warmInterpreter { endedCalls.insert(warm.callID) }
        if let callID { endedCalls.insert(callID) }
        dropPrewarmed()
        dropWarmInterpreter()
        stopSessions()
        saveRecord(summarizing: true)
        callID = nil
        pendingGoal = nil
        pendingLanguage = nil
    }

    // MARK: records

    private var usedModes: Set<String> = []
    private var callStartedAt: Date?
    private var callNumber: String?
    private var summarizedCalls: Set<String> = []
    /// Hung up calls: the backend may still list one for a moment.
    private var endedCalls: Set<String> = []

    /// Keeps the bilingual captions (and later the summary) after the call.
    private func saveRecord(summarizing: Bool) {
        guard let callID, captions.contains(where: { $0.speaker != .note }) else { return }
        let record = AICallRecord(
            id: callID,
            date: callStartedAt ?? Date(),
            number: callNumber,
            modes: usedModes.sorted(),
            lines: captions.map { .init(who: $0.speaker.label, text: $0.text, chinese: $0.chinese) },
            summary: summary
        )
        AICallHistory.save(record)
        guard summarizing, summary == nil, !summarizedCalls.contains(callID),
              captions.contains(where: { $0.speaker == .them }) else { return }
        summarizedCalls.insert(callID)
        summarize(record)
    }

    private func stopSessions() {
        sessions.forEach { $0.close() }
        sessions = []
        toneCap?.cancel()
        audio?.onRemoteAudio = nil
        audio?.onLocalAudio = nil
        audio?.onAssistantBacklogDropped = nil
        for key in Array(drafts.keys) { closeDraft(key) }
        phase = ""
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

    /// Captions still being written, by stream ("in.heard", "out.said" …).
    private var drafts: [String: UUID] = [:]

    private func index(of id: UUID?) -> Int? {
        guard let id else { return nil }
        return captions.lastIndex { $0.id == id }
    }

    /// The last caption of this speaker if it is one of the newest two and
    /// still misses this part (the translation can come before the transcript).
    private func recent(_ speaker: AICaption.Speaker, missing chinese: Bool) -> Int? {
        guard let i = captions.lastIndex(where: { $0.speaker == speaker }), i >= captions.count - 2 else { return nil }
        let empty = chinese ? (captions[i].chinese ?? "").isEmpty : captions[i].text.isEmpty
        return empty ? i : nil
    }

    /// Shows words as they are recognised instead of after the sentence.
    private func draft(_ key: String, _ speaker: AICaption.Speaker, chinese: Bool = false, _ delta: String) {
        guard mode != .off else { return }
        if let i = index(of: drafts[key]) {
            if chinese { captions[i].chinese = (captions[i].chinese ?? "") + delta } else { captions[i].text += delta }
            return
        }
        let target: Int
        if chinese, let i = index(of: drafts["in.heard"]) ?? recent(speaker, missing: true) {
            target = i
            captions[i].chinese = (captions[i].chinese ?? "") + delta
        } else if !chinese, let i = recent(speaker, missing: false) {
            target = i
            captions[i].text += delta
        } else {
            captions.append(chinese ? AICaption(speaker: speaker, text: "", chinese: delta)
                                    : AICaption(speaker: speaker, text: delta))
            if captions.count > 200 { captions.removeFirst(captions.count - 200) }
            target = captions.count - 1
        }
        captions[target].live = true
        drafts[key] = captions[target].id
    }

    /// The final text replaces the draft.
    private func settle(_ key: String, _ speaker: AICaption.Speaker, chinese: Bool = false,
                        _ text: String, translate: Bool = false) {
        guard let i = index(of: drafts.removeValue(forKey: key)) else {
            if chinese { attachChinese(text, to: speaker) } else { add(speaker, text, translate: translate) }
            return
        }
        if chinese { captions[i].chinese = text } else { captions[i].text = text }
        captions[i].live = drafts.values.contains(captions[i].id)
        if translate, !chinese, !looksChinese(text) { translateCaption(captions[i].id, text) }
    }

    /// Ends a draft as it stands (e.g. the AI was cut off).
    private func closeDraft(_ key: String) {
        guard let i = index(of: drafts.removeValue(forKey: key)) else { return }
        captions[i].live = drafts.values.contains(captions[i].id)
    }

    private func translateCaption(_ id: UUID, _ text: String) {
        Task {
            if let zh = try? await chat("把下面这句电话里的话翻译成简体中文，只输出译文：\n\(text)") {
                if let i = captions.firstIndex(where: { $0.id == id }) { captions[i].chinese = zh }
            }
        }
    }

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
        translateCaption(captions.last!.id, text)
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

    private func summarize(_ record: AICallRecord) {
        let interpreted = record.modes == ["AI 翻译"]
        let transcript = record.lines.compactMap { l -> String? in
            switch l.who {
            case "对方":
                if let zh = l.chinese, !zh.isEmpty, zh != l.text { return "对方：\(l.text)（中文：\(zh)）" }
                return "对方：\(l.text)"
            case "AI": return interpreted ? nil : "AI：\(l.text)"
            case "我": return "我：\(l.text)"
            default: return nil
            }
        }.joined(separator: "\n")
        guard !transcript.isEmpty else { return }
        let goalLine = goal.isEmpty || interpreted ? "" : "我交代的事：\(goal)\n"
        let what = interpreted ? "我和对方通过 AI 翻译打的一通电话" : "AI 替我打/接的一通电话"
        let id = record.id
        Task {
            guard let text = try? await chat("""
            下面是\(what)。\(goalLine)用简体中文简短总结：对方是谁、说了什么、结果如何、我接下来要做什么。用 3–5 条短句。

            \(transcript)
            """) else { return }
            AICallHistory.update(id) { $0.summary = text }
            if callID == id || callID == nil { summary = text }
            SystemNotifications.shared?.post(title: "AI 通话总结", body: text)
        }
    }

    // MARK: text model

    private func chat(_ prompt: String) async throws -> String {
        if settings.usesGemini {
            return try await GeminiLiveSession.generate(key: settings.geminiKey,
                                                        model: settings.geminiTextModel, prompt: prompt)
        }
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
        let session = makeSession()
        let modelName = s.usesGemini ? s.geminiModel : s.realtimeModel
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
            connect(session, instructions: "Reply with one short word.", interrupt: true, transcribe: false)
            session.requestResponse("Say: hello.")
            DispatchQueue.global().asyncAfter(deadline: .now() + 20) {
                finish("实时语音 20 秒没回应（网络慢或模型 \(modelName) 不可用）")
            }
        }
    }

    /// Checks the key and address with a tiny request.
    static func testConnection() async -> String {
        let s = AICallSettings.shared
        if s.usesGemini {
            guard s.isConfigured,
                  let url = URL(string: "https://generativelanguage.googleapis.com/v1beta/models/\(s.geminiModel)") else {
                return "还没填 Gemini API Key"
            }
            var request = URLRequest(url: url)
            request.timeoutInterval = 15
            request.setValue(s.geminiKey, forHTTPHeaderField: "x-goog-api-key")
            do {
                let (_, response) = try await URLSession.shared.data(for: request)
                switch (response as? HTTPURLResponse)?.statusCode ?? 0 {
                case 200: return await realtimeVoiceCheck()
                case 400, 401, 403: return "Gemini API Key 不对或没开通"
                case 404: return "找不到模型 \(s.geminiModel)"
                case let code: return "Gemini 返回 \(code)"
                }
            } catch {
                return "连不上 Gemini：\(error.localizedDescription)（在国内需要代理）"
            }
        }
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

    /// Marks "still sounding now" (the 0.4 s echo tail follows).
    func hold() {
        lock.lock(); defer { lock.unlock() }
        until = max(until, Date())
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
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let stamp = f.string(from: Date())
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

/// 24 kHz -> 8 kHz for the phone line with a proper low-pass first, so the AI's
/// voice comes through clean instead of buzzy (plain averaging aliases).
final class PhoneDecimator: @unchecked Sendable {
    private static let taps: [Float] = {
        let n = 31, fc: Float = 3_600 / 24_000
        var h = [Float](repeating: 0, count: n)
        for i in 0 ..< n {
            let m = Float(i - n / 2)
            let sinc = m == 0 ? 2 * fc : sin(2 * .pi * fc * m) / (.pi * m)
            let w = 0.54 - 0.46 * cos(2 * .pi * Float(i) / Float(n - 1))
            h[i] = sinc * w
        }
        let sum = h.reduce(0, +)
        return h.map { $0 / sum }
    }()
    private let lock = NSLock()
    private var history = [Float](repeating: 0, count: 30)
    private var phase = 0

    func process(_ data: Data) -> Data {
        lock.lock(); defer { lock.unlock() }
        let n = data.count / 2
        guard n > 0 else { return Data() }
        var x = history
        x.reserveCapacity(history.count + n)
        data.withUnsafeBytes { raw in
            let s = raw.bindMemory(to: Int16.self)
            for i in 0 ..< n { x.append(Float(Int16(littleEndian: s[i]))) }
        }
        let taps = Self.taps
        var out: [Int16] = []
        out.reserveCapacity(n / 3 + 1)
        var i = history.count + phase
        while i < x.count {
            var acc: Float = 0
            let start = i - (taps.count - 1)
            for k in 0 ..< taps.count { acc += taps[k] * x[start + k] }
            out.append(Int16(max(-32_768, min(32_767, acc))))
            i += 3
        }
        phase = i - x.count
        history = Array(x.suffix(30))
        return out.withUnsafeBytes { Data($0) }
    }
}
