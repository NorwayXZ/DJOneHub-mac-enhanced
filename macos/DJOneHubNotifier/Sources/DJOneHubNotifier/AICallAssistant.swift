import Foundation
import Security
import SwiftUI

// MARK: - Settings

/// Where the call AI lives and how it sounds.
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
    /// "simultaneous": OpenAI's live interpreter speaks while you talk.
    /// "turn": translates each sentence after you finish (can tidy up wording).
    @AppStorage("ai.translateStyle") var translateStyle = "simultaneous"
    /// Headset mode: disables the speaker echo-gate so you can speak naturally
    /// while hearing the Chinese translation, and switches OpenAI noise reduction
    /// to `near_field`.
    @AppStorage("ai.headsetMode") var headsetMode = false
    /// Original caller voice volume underneath the Chinese translation (0.0 – 0.6).
    @AppStorage("ai.callerMonitorVolume") var callerMonitorVolume: Double = 0.18
    /// Automatically refresh the rolling business summary while on a call.
    @AppStorage("ai.autoLiveSummary") var autoLiveSummary = true
    /// Auto-detect caller language at the start of a call. Off by default so
    /// accented English doesn't unexpectedly flip mid-call.
    @AppStorage("ai.autoDetectLanguage") var autoDetectLanguage = false

    var usesSimultaneous: Bool { !usesGemini && translateStyle == "simultaneous" }

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
        base = base.replacingOccurrences(of: "platform.openai.com", with: "api.openai.com")
        return base.isEmpty ? "https://api.openai.com" : base
    }
}

/// Remembers each customer phone number's chosen language across calls.
enum CallLanguageMemory {
    private static let key = "ai.languageByNumber"

    static func normalizedKey(for number: String?) -> String? {
        guard let number else { return nil }
        let norm = ContactStore.normalized(number)
        guard !norm.isEmpty else { return nil }
        return norm.count >= 7 ? String(norm.suffix(9)) : norm
    }

    static func language(for number: String?) -> CallLanguage? {
        guard let k = normalizedKey(for: number),
              let dict = UserDefaults.standard.dictionary(forKey: key) as? [String: String],
              let id = dict[k] else { return nil }
        return CallLanguage.find(id)
    }

    static func remember(_ language: CallLanguage, for number: String?) {
        guard let k = normalizedKey(for: number), !language.id.contains("Chinese") else { return }
        var dict = (UserDefaults.standard.dictionary(forKey: key) as? [String: String]) ?? [:]
        dict[k] = language.id
        UserDefaults.standard.set(dict, forKey: key)
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

    /// ISO 639-1 code for the live interpreter.
    var code: String {
        let codes = [
            "English": "en", "Japanese": "ja", "Korean": "ko", "German": "de", "French": "fr",
            "Spanish": "es", "Italian": "it", "Portuguese": "pt", "Russian": "ru", "Dutch": "nl",
            "Polish": "pl", "Turkish": "tr", "Arabic": "ar", "Hebrew": "he", "Thai": "th",
            "Vietnamese": "vi", "Indonesian": "id", "Malay": "ms", "Hindi": "hi", "Swedish": "sv",
            "Norwegian": "no", "Danish": "da", "Finnish": "fi", "Greek": "el", "Czech": "cs",
            "Hungarian": "hu", "Romanian": "ro", "Ukrainian": "uk", "Cantonese": "zh",
            "Mandarin Chinese": "zh",
        ]
        return codes[id] ?? "en"
    }

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

    /// Picks remembered customer language first, then guesses from country code (default English).
    static func preferred(for number: String?) -> CallLanguage {
        if let saved = CallLanguageMemory.language(for: number) { return saved }
        let guessed = guess(for: number)
        return guessed.id == "Mandarin Chinese" ? find("English") : guessed
    }

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

    /// Normalized RMS energy (0.0 – 1.0) of little-endian PCM16 audio.
    static func rms16(_ data: Data) -> Double {
        let n = data.count / 2
        guard n > 0 else { return 0 }
        var sum = 0.0
        data.withUnsafeBytes { raw in
            let s = raw.bindMemory(to: Int16.self)
            for i in 0 ..< n {
                let v = Double(Int16(littleEndian: s[i])) / 32_768.0
                sum += v * v
            }
        }
        return sqrt(sum / Double(n))
    }
}

/// Thread-safe boolean flag shared between UI (@MainActor) and audio capture queue.
final class AtomicBool: @unchecked Sendable {
    private let lock = NSLock()
    private var flag: Bool
    init(_ initial: Bool) { flag = initial }
    var value: Bool {
        get { lock.lock(); defer { lock.unlock() }; return flag }
        set { lock.lock(); flag = newValue; lock.unlock() }
    }
}

// MARK: - Realtime session (turn-by-turn / agent)

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
    private var pending24k = Data()
    private var closed = false
    private var leftover = Data()   // odd byte carry for 24 kHz output
    private var baseInstructions = ""
    private var sessionConfig: [String: Any] = [:]
    private var updateSent = false
    private var ready = false
    private var queuedResponse: [String: Any]?
    private var answerAfterCurrent = false
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
        queue.asyncAfter(deadline: .now() + 3) { [weak self] in
            guard let self, !self.closed, !self.updateSent else { return }
            self.updateSent = true
            self.send(["type": "session.update", "session": self.sessionConfig])
        }
        queue.asyncAfter(deadline: .now() + 6) { [weak self] in
            guard let self, !self.closed, !self.ready else { return }
            AICallLog.write("\(self.tag) no session.updated after 6s, going ahead")
            self.ready = true
            if let r = self.queuedResponse { self.queuedResponse = nil; self.send(r) }
        }
    }

    /// Adds 8 kHz call audio; sent in ~60 ms batches.
    func appendAudio8k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending.append(pcm)
            guard self.pending.count >= 960 else { return }
            let chunk = PCM.up3(self.pending)
            self.pending.removeAll(keepingCapacity: true)
            self.send(["type": "input_audio_buffer.append", "audio": chunk.base64EncodedString()])
        }
    }

    /// Adds native 24 kHz microphone audio directly; sent in ~60 ms batches.
    func appendAudio24k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending24k.append(pcm)
            guard self.pending24k.count >= 2_880 else { return }
            let chunk = self.pending24k
            self.pending24k.removeAll(keepingCapacity: true)
            self.send(["type": "input_audio_buffer.append", "audio": chunk.base64EncodedString()])
        }
    }

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
                queue.async { [weak self] in self?.answerAfterCurrent = true }
            } else if Self.harmlessErrors.contains(code) {
                break
            } else if queue.sync(execute: { ready }) {
                AICallLog.write("\(tag) non-fatal error \(code): \(msg)")
            } else {
                onError?("AI 出错：\(msg)")
            }
        default:
            break
        }
    }

    static func config(
        instructions: String,
        voice: String,
        interrupt: Bool,
        transcribe: Bool = true,
        eagerness: String = "high"
    ) -> [String: Any] {
        var input: [String: Any] = [
            "format": ["type": "audio/pcm", "rate": 24_000],
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

/// One entry in the live call log.
struct AICaption: Identifiable, Equatable {
    enum Speaker { case them, me, ai, note }
    let id = UUID()
    let speaker: Speaker
    var text: String
    /// For `.them` and `.ai`: Chinese translation.
    var chinese: String?
    /// For `.me` in translate mode: foreign translation spoken to the caller.
    var translation: String?
    var timestamp = Date()
    /// Still being spoken: words appear as they are recognised.
    var live = false
}

@MainActor
final class AICallAssistant: ObservableObject {
    enum Mode: Equatable { case off, translate, agent }

    @Published private(set) var mode: Mode = .off
    @Published private(set) var captions: [AICaption] = []
    @Published var language: CallLanguage = CallLanguage.find("English")
    @Published private(set) var status = ""
    @Published private(set) var phase = ""
    /// Live rolling structured summary updated throughout the call.
    @Published private(set) var liveSummary: StructuredCallSummary?
    @Published private(set) var isSummarizing = false
    @Published private(set) var summaryError: String?
    /// Legacy plain-text summary (kept in sync with `liveSummary`).
    @Published private(set) var summary: String?
    /// Most recent previous AI call with this same customer number.
    @Published private(set) var previousCall: AICallRecord?
    @Published private(set) var lastError: String?
    @Published private(set) var directMicPassthrough = false
    @Published private(set) var isSimultaneousActive = false
    @Published var autoLanguage: Bool = AICallSettings.shared.autoDetectLanguage {
        didSet { AICallSettings.shared.autoDetectLanguage = autoLanguage }
    }

    /// Pending actions chosen on the dial pad before connecting.
    var pendingGoal: String?
    var pendingLanguage: CallLanguage?
    var pendingTranslate = false

    private var sessions: [any VoiceAISession] = []
    private weak var audio: VoiceAudioService?
    private var callID: String?
    private var goal = ""
    private let settings = AICallSettings.shared
    private let headsetFlag = AtomicBool(false)

    // Rolling summary scheduling state
    private var settledTurnsSinceSummary = 0
    private var lastSummaryAt: Date = .distantPast
    private var summaryTask: Task<Void, Never>?
    private var autoDetectedOnce = false

    var isOn: Bool { mode != .off }

    // MARK: Prompts

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

    /// Both interpreter connections (optionally prewarmed for "带同传拨打").
    @MainActor private final class InterpreterPair {
        let inbound: any VoiceAISession
        let outbound: any VoiceAISession
        var language: String
        let callID: String
        let simultaneous: Bool
        var broken = false
        init(inbound: any VoiceAISession, outbound: any VoiceAISession, language: String, callID: String,
             simultaneous: Bool) {
            self.inbound = inbound; self.outbound = outbound; self.language = language; self.callID = callID
            self.simultaneous = simultaneous
        }
        func retarget(_ lang: String) {
            language = lang
            if let live = outbound as? TranslateStreamSession {
                live.setLanguage(CallLanguage.find(lang).code)
            } else {
                outbound.updateInstructions(AICallAssistant.outboundPrompt(base: AICallAssistant.interpreterBase,
                                                                          language: lang))
            }
        }
        func setHeadsetMode(_ headset: Bool) {
            if let liveOut = outbound as? TranslateStreamSession {
                liveOut.setNoiseReduction(headset ? "near_field" : "far_field")
            }
        }
        func close() { inbound.close(); outbound.close() }
    }

    private var warmInterpreter: InterpreterPair?
    private var interpreterPair: InterpreterPair?
    private var simultaneousUnavailable = false

    /// Loads the customer's previous AI call brief so you immediately remember
    /// what you discussed last time as soon as the call rings or connects.
    func loadPreviousCall(for number: String?, excludingID: String? = nil) {
        previousCall = AICallHistory.latestRecord(matchingNumber: number, excludingID: excludingID)
    }

    private func makeInterpreter(language lang: String, callID: String) -> InterpreterPair {
        let live = settings.usesSimultaneous && !simultaneousUnavailable
        if live {
            let inbound = TranslateStreamSession()
            let outbound = TranslateStreamSession()
            inbound.tag = "in"
            outbound.tag = "out"
            let pair = InterpreterPair(inbound: inbound, outbound: outbound, language: lang, callID: callID,
                                       simultaneous: true)
            for s in [pair.inbound, pair.outbound] {
                s.onError = { [weak pair] e in
                    AICallLog.write("warm interpreter error: \(e)")
                    Task { @MainActor in pair?.broken = true }
                }
            }
            let outNR = settings.headsetMode ? "near_field" : "far_field"
            AICallLog.write("timing: AI connecting (live simultaneous → \(lang), outNR=\(outNR))")
            // Inbound comes from 8 kHz cellular line: far_field helps strip line noise.
            inbound.connect(base: settings.trimmedBase, key: settings.apiKey, language: "zh",
                            transcribe: true, noiseReduction: "far_field")
            outbound.connect(base: settings.trimmedBase, key: settings.apiKey,
                             language: CallLanguage.find(lang).code,
                             transcribe: true, noiseReduction: outNR)
            return pair
        }
        let pair = InterpreterPair(inbound: Self.makeSession(), outbound: Self.makeSession(),
                                   language: lang, callID: callID, simultaneous: false)
        for s in [pair.inbound, pair.outbound] {
            s.onError = { [weak pair] e in
                AICallLog.write("warm interpreter error: \(e)")
                Task { @MainActor in pair?.broken = true }
            }
        }
        pair.inbound.tag = "in"
        pair.outbound.tag = "out"
        connect(pair.inbound, instructions: Self.inboundPrompt, interrupt: false, eagerness: "high")
        connect(pair.outbound, instructions: Self.outboundPrompt(base: Self.interpreterBase, language: lang),
                interrupt: false, eagerness: "high")
        return pair
    }

    /// Prewarms the interpreter ONLY when the user explicitly dialed with AI translate.
    func prewarmInterpreter(callID: String, number: String?, preferredLanguage: CallLanguage? = nil) {
        guard settings.isConfigured, mode == .off, prewarmed == nil, pendingGoal == nil,
              warmInterpreter?.callID != callID, !endedCalls.contains(callID) else { return }
        warmInterpreter?.close()
        let lang = preferredLanguage ?? CallLanguage.preferred(for: number)
        warmInterpreter = makeInterpreter(language: lang.id, callID: callID)
        AICallLog.write("timing: prewarming interpreter (\(lang.id))")
    }

    private func dropWarmInterpreter() {
        warmInterpreter?.close()
        warmInterpreter = nil
    }

    func startTranslate(callID: String, number: String?, audio: VoiceAudioService) {
        guard prepare(callID: callID, number: number, audio: audio) else { return }
        if language.id == "Mandarin Chinese" { language = CallLanguage.preferred(for: number) }
        let lang = language.id
        CallLanguageMemory.remember(language, for: number)
        headsetFlag.value = settings.headsetMode
        audio.setCallerMonitorVolume(Float(settings.callerMonitorVolume))

        AICallLog.write("=== START TRANSLATE call=\(callID) lang=\(lang) style=\(settings.translateStyle) headset=\(settings.headsetMode) ===")

        let pair: InterpreterPair
        if let warm = warmInterpreter, warm.callID == callID, !warm.broken,
           warm.simultaneous == (settings.usesSimultaneous && !simultaneousUnavailable) {
            pair = warm
            AICallLog.write("timing: using prewarmed interpreter")
            if warm.language != lang { warm.retarget(lang) }
            warm.setHeadsetMode(settings.headsetMode)
        } else {
            warmInterpreter?.close()
            pair = makeInterpreter(language: lang, callID: callID)
        }
        warmInterpreter = nil
        let inbound = pair.inbound
        let outbound = pair.outbound
        let live = pair.simultaneous
        isSimultaneousActive = live

        // Only hold back the mic when on speakers; headset mode runs true full-duplex.
        let gate = SpeakerGate()
        let headset = headsetFlag

        inbound.onAudio = { [weak audio] pcm in audio?.playAssistant(pcm) }
        inbound.onSpeechStarted = { [weak self] in Task { @MainActor in self?.setPhase("客户正在说…") } }
        inbound.onHeardDelta = { [weak self] d in Task { @MainActor in self?.draft("in.heard", .them, d) } }
        inbound.onSaidDelta = { [weak self] d in Task { @MainActor in
            self?.setPhase(live ? "同声传译中 · 正在听客户" : "正在翻成中文给你听")
            self?.draft("in.said", .them, chinese: true, d)
        } }
        inbound.onHeard = { [weak self] t in Task { @MainActor in
            self?.settle("in.heard", .them, t)
            self?.noticeLanguage(of: t)
        } }
        inbound.onSaid = { [weak self] t in Task { @MainActor in
            self?.settle("in.said", .them, chinese: true, t)
            self?.setPhase(live ? "同声传译中 · 直接对话" : "轮到你说")
        } }

        let outDecimator = PhoneDecimator()
        outbound.onAudio = { [weak audio] pcm in
            audio?.sendAssistantSpeech(outDecimator.process(pcm))
        }
        outbound.onSpeechStarted = { [weak self] in
            Task { @MainActor in self?.setPhase(live ? "同声传译中 · 正在翻给客户" : "你在说…") }
        }
        outbound.onSpeechStopped = { [weak self] in
            Task { @MainActor in if !live { self?.setPhase("正在翻给客户…") } }
        }
        // Merge user's Chinese ("out.heard") and AI's foreign translation ("out.said")
        // into the same right-hand `.me` bubble so the chat log is clean and symmetric.
        outbound.onHeardDelta = { [weak self] d in Task { @MainActor in self?.draft("out.heard", .me, d) } }
        outbound.onSaidDelta = { [weak self] d in Task { @MainActor in
            self?.setPhase(live ? "同声传译中 · 正在说给客户听" : "正在把你的话说给客户听")
            self?.draft("out.said", .me, outboundTranslation: true, d)
        } }
        outbound.onHeard = { [weak self] t in Task { @MainActor in self?.settle("out.heard", .me, t) } }
        outbound.onSaid = { [weak self] t in
            Task { @MainActor in
                self?.settle("out.said", .me, outboundTranslation: true, t)
                self?.setPhase(live ? "同声传译中 · 直接对话" : "等客户回话")
            }
        }
        for s in [inbound, outbound] {
            s.onError = { [weak self] e in Task { @MainActor in
                if live { self?.liveInterpreterFailed(e) } else { self?.fail(e) }
            } }
        }

        // Caller (8 kHz cellular) -> inbound interpreter
        audio.onRemoteAudio = { [weak inbound] pcm in inbound?.appendAudio8k(pcm) }
        // User's Mac mic (wideband 24 kHz!) -> outbound interpreter
        let micSpeechLogged = AtomicBool(false)
        audio.onLocalAudio = nil
        audio.onLocalAudio24k = { [weak outbound, weak audio] pcm24 in
            if !headset.value {
                if audio?.isAssistantSounding == true { gate.hold() }
                if gate.isSpeaking {
                    if live { outbound?.appendAudio24k(Data(count: pcm24.count)) }
                    return
                }
            }
            if !micSpeechLogged.value {
                let r = PCM.rms16(pcm24)
                if r >= 0.015 {
                    micSpeechLogged.value = true
                    AICallLog.write(String(format: "out mic: first voice forwarded rms=%.4f", r))
                }
            }
            outbound?.appendAudio24k(pcm24)
        }
        audio.onAssistantBacklogDropped = { [weak self] in Task { @MainActor in
            self?.add(.note, "对方说得太长，中文语音落后太多，跳过了一段；请看聊天记录")
        } }

        interpreterBase = Self.interpreterBase
        sessions = [inbound, outbound]
        outboundSession = outbound
        directMicPassthrough = false
        audio.setDirectMicPassthrough(false)
        audio.setAIRouting(.translate)
        mode = .translate
        usedModes.insert(live ? "AI 同传" : "AI 翻译")
        interpreterPair = pair
        updateTranslateStatus()
        setPhase(live ? "同声传译中 · 直接对话" : "请说，或等对方说")
    }

    /// Toggles headset full-duplex mode live during a call.
    func setHeadsetMode(_ enabled: Bool) {
        settings.headsetMode = enabled
        headsetFlag.value = enabled
        interpreterPair?.setHeadsetMode(enabled)
        if mode == .translate {
            add(.note, enabled ? "已切到耳机模式：边听边说不闭麦（近场降噪）" : "已切到外放模式：播中文时防回声（远场降噪）")
        }
    }

    /// Adjusts how loudly the caller's original voice plays underneath the Chinese translation.
    func setCallerMonitorVolume(_ value: Double) {
        settings.callerMonitorVolume = value
        audio?.setCallerMonitorVolume(Float(value))
    }

    /// Routes your raw voice straight to the caller (e.g. to say "OK, thank you" directly).
    func setDirectMicPassthrough(_ enabled: Bool) {
        guard mode == .translate else { return }
        directMicPassthrough = enabled
        audio?.setDirectMicPassthrough(enabled)
        if enabled {
            setPhase("原声直说中 · 你的声音直接发给对方")
        } else {
            setPhase(isSimultaneousActive ? "同声传译中 · 直接对话" : "请说，或等对方说")
        }
    }

    /// Drops backed-up outbound foreign speech if you want to cut off a long sentence.
    func clearOutboundQueue() {
        audio?.clearAssistantUplink()
        add(.note, "已跳过待播出的外语队列")
    }

    private func liveInterpreterFailed(_ message: String) {
        guard mode == .translate, let callID, let audio else { return }
        AICallLog.write("live interpreter failed, falling back: \(message)")
        simultaneousUnavailable = true
        stopSessions()
        add(.note, "同声传译暂时用不了（\(message)），已自动切到逐句翻译")
        startTranslate(callID: callID, number: callNumber, audio: audio)
    }

    private func setPhase(_ text: String) {
        guard mode == .translate, phase != text else { return }
        phase = text
    }

    // MARK: Language selection & optional one-shot auto-detect

    private var interpreterBase = ""
    private weak var outboundSession: (any VoiceAISession)?
    private var detecting = false

    private func updateTranslateStatus() {
        let modeText = isSimultaneousActive ? "同声传译（边说边翻）" : "逐句翻译"
        let who = autoLanguage && !autoDetectedOnce
            ? "\(language.name)（开头自动识别）"
            : language.name
        status = "\(modeText) · 你说中文 → 客户听\(who) · 客户说的话 → 你听中文"
    }

    /// Switches the interpreter to another target language mid-call and remembers it for this number.
    func setTargetLanguage(_ lang: CallLanguage, auto: Bool = false) {
        if !auto {
            CallLanguageMemory.remember(lang, for: callNumber)
            autoDetectedOnce = true
        }
        if auto && lang == language {
            autoDetectedOnce = true
            return
        }
        language = lang
        if mode == .translate, outboundSession != nil, let pair = interpreterPair {
            pair.retarget(lang.id)
            if auto {
                autoDetectedOnce = true
                add(.note, "识别到客户说\(lang.name)，已锁定输出为\(lang.name)（可手动改）")
            } else {
                add(.note, "客户语言已切为\(lang.name)")
            }
            updateTranslateStatus()
        }
    }

    fileprivate func noticeLanguage(of text: String) {
        // Lock once identified so accented English never jumps mid-call.
        guard autoLanguage, !autoDetectedOnce, mode == .translate, !detecting else { return }
        if let lang = CallLanguage.byScript(text) {
            if lang.id != "Mandarin Chinese" { setTargetLanguage(lang, auto: true) }
            return
        }
        if looksChinese(text) { return }
        let words = text.split(separator: " ").count
        guard words >= 3 || text.count >= 14 else { return }
        detecting = true
        Task {
            defer { detecting = false }
            let names = CallLanguage.all.map(\.id).joined(separator: ", ")
            guard let answer = try? await Self.chat("Which language is this phone-call sentence in? Answer with exactly one name from this list and nothing else: \(names).\n\n\(text)") else { return }
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

    // MARK: AI Agent (代打 / 接听)

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
        audio.onLocalAudio24k = nil
        sessions = [s]
        audio.setAIRouting(.agent)
        mode = .agent
        isSimultaneousActive = false
        status = screening ? "AI 正在替你接听，问清对方是谁、什么事" : "AI 正在替你通话"
        if speakFirst {
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
            liveSummary = nil
            summary = nil
            summaryError = nil
            settledTurnsSinceSummary = 0
            lastSummaryAt = .distantPast
            autoDetectedOnce = false
            usedModes = []
            callStartedAt = Date()
            loadPreviousCall(for: number, excludingID: callID)
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

    // MARK: Stop & Hang-up

    func takeOver() {
        stopSessions()
        add(.note, "你接过了电话")
        saveRecord(summarizing: true, notify: false)
    }

    func callEnded() {
        if let warm = warmInterpreter { endedCalls.insert(warm.callID) }
        if let callID { endedCalls.insert(callID) }
        dropPrewarmed()
        dropWarmInterpreter()
        stopSessions()
        saveRecord(summarizing: true, notify: true)
        callID = nil
        pendingGoal = nil
        pendingLanguage = nil
        pendingTranslate = false
        previousCall = nil
    }

    // MARK: Live Rolling Summary & Records

    private var usedModes: Set<String> = []
    private var callStartedAt: Date?
    private var callNumber: String?
    private var endedCalls: Set<String> = []

    private func currentRecordSnapshot() -> AICallRecord? {
        guard let callID, captions.contains(where: { $0.speaker != .note }) else { return nil }
        let secs = callStartedAt.map { max(1, Int(Date().timeIntervalSince($0))) }
        return AICallRecord(
            id: callID,
            date: callStartedAt ?? Date(),
            number: callNumber,
            modes: usedModes.sorted(),
            lines: captions.map {
                .init(
                    who: $0.speaker.label,
                    text: $0.text,
                    chinese: $0.chinese,
                    translation: $0.translation,
                    timestamp: $0.timestamp
                )
            },
            summary: liveSummary?.plainText ?? summary,
            durationSeconds: secs,
            language: language.name,
            structuredSummary: liveSummary
        )
    }

    private func saveRecord(summarizing: Bool, notify: Bool) {
        guard let record = currentRecordSnapshot() else { return }
        AICallHistory.save(record)
        guard summarizing else { return }
        let spokenCount = record.lines.filter { $0.who != "提示" }.count
        guard spokenCount >= 1 else { return }
        runFinalSummary(for: record, notify: notify)
    }

    /// Manually triggers a live summary update while on the call.
    func refreshLiveSummaryNow() {
        guard let record = currentRecordSnapshot() else { return }
        triggerRollingSummary(for: record, force: true)
    }

    private func maybeTriggerRollingSummary() {
        guard settings.autoLiveSummary, mode != .off, !isSummarizing else { return }
        settledTurnsSinceSummary += 1
        let realTurns = captions.filter { $0.speaker != .note }.count
        let elapsed = Date().timeIntervalSince(lastSummaryAt)
        let shouldRun: Bool
        if liveSummary == nil {
            shouldRun = realTurns >= 3 && elapsed >= 12
        } else {
            shouldRun = (settledTurnsSinceSummary >= 4 && elapsed >= 20)
                || (settledTurnsSinceSummary >= 2 && elapsed >= 45)
        }
        guard shouldRun, let record = currentRecordSnapshot() else { return }
        triggerRollingSummary(for: record, force: false)
    }

    private func triggerRollingSummary(for record: AICallRecord, force: Bool) {
        if isSummarizing && !force { return }
        summaryTask?.cancel()
        isSummarizing = true
        summaryError = nil
        let existing = liveSummary
        let currentCallID = record.id
        summaryTask = Task { [weak self] in
            let result = await Self.summarizeRecord(record, previousSummary: existing, live: true)
            await MainActor.run {
                guard let self else { return }
                self.isSummarizing = false
                if let result {
                    self.lastSummaryAt = Date()
                    self.settledTurnsSinceSummary = 0
                    if self.callID == currentCallID {
                        self.liveSummary = result
                        self.summary = result.plainText
                        if let snap = self.currentRecordSnapshot() { AICallHistory.save(snap) }
                    }
                } else if force {
                    self.summaryError = "暂时没整理出来，可以稍后再点一次"
                }
            }
        }
    }

    private func runFinalSummary(for record: AICallRecord, notify: Bool) {
        let existing = liveSummary
        let id = record.id
        isSummarizing = true
        Task { [weak self] in
            let result = await Self.summarizeRecord(record, previousSummary: existing, live: false)
            await MainActor.run {
                guard let self else { return }
                self.isSummarizing = false
                guard let result else { return }
                AICallHistory.update(id) {
                    $0.structuredSummary = result
                    $0.summary = result.plainText
                }
                if self.callID == id || self.callID == nil {
                    self.liveSummary = result
                    self.summary = result.plainText
                }
                if notify {
                    SystemNotifications.shared?.post(title: "AI 谈话概要已生成", body: result.notificationBody)
                }
            }
        }
    }

    /// Formats a call record into a clean transcript for the summary model.
    static func transcriptText(for record: AICallRecord, maxLines: Int = 80) -> String {
        let relevant = record.lines.filter { $0.who != "提示" }.suffix(maxLines)
        return relevant.compactMap { l -> String? in
            switch l.who {
            case "对方":
                if let zh = l.chinese, !zh.isEmpty, zh != l.text {
                    return l.text.isEmpty ? "客户：\(zh)" : "客户：\(zh)（原文：\(l.text)）"
                }
                return l.text.isEmpty ? nil : "客户：\(l.text)"
            case "我":
                let text = l.text.isEmpty ? (l.translation ?? "") : l.text
                return text.isEmpty ? nil : "我：\(text)"
            case "AI":
                if let zh = l.chinese, !zh.isEmpty { return "AI替我说：\(zh)" }
                return l.text.isEmpty ? nil : "AI替我说：\(l.text)"
            default:
                return nil
            }
        }.joined(separator: "\n")
    }

    /// Generates a structured Chinese summary for either a live call or a saved record.
    static func summarizeRecord(
        _ record: AICallRecord,
        previousSummary: StructuredCallSummary? = nil,
        live: Bool = false
    ) async -> StructuredCallSummary? {
        let transcript = transcriptText(for: record)
        guard !transcript.isEmpty else { return nil }
        let prevBlock: String
        if let prev = previousSummary, !prev.isEmpty {
            prevBlock = "\n\n【之前已整理的前半段概要（请保留前面仍有效的重要信息，把新对话合并进去，不要丢失前面的数字、人名、价格、时间）】\n\(prev.plainText)\n"
        } else {
            prevBlock = ""
        }
        let stageNote = live
            ? "这通电话【正在进行中】，用户需要扫一眼就知道前面谈了什么、哪些定了、哪些还没定，方便接着往下谈。"
            : "这通电话【已经结束】，请整理成一份完整的中文谈话纪要，方便日后查阅和跟进。"
        let prompt = """
        你是一位极简高效的商务中文秘书。\(stageNote)
        请根据下面的通话内容，输出一个严格的 JSON 对象（不要任何多余解释、不要 Markdown 以外的闲话），字段如下：
        {
          "topic": "一句话主题（25字以内，说清在谈什么事）",
          "highlights": ["前面已经讲过的重要事实/背景/报价/需求（每条15-35字，精确保留所有数字、金额、时间、人名、地点）"],
          "agreed": ["双方已经确定或达成一致的事（没有就留空数组 []）"],
          "open_questions": ["还没定下来、存在分歧、或对方问了还没回答的事（没有就留空数组 []）"],
          "next_steps": ["接下来需要确认或跟进的具体动作（没有就留空数组 []）"]
        }
        要求：
        1. 全部用简体中文，大白话、短句、一眼能扫完，每组最多 5 条。
        2. 只写对话里真实出现过的信息，绝不编造。
        3. 重要数字、金额、日期、人名、公司名必须原样保留。\(prevBlock)

        【对话记录】
        \(transcript)
        """
        guard let reply = try? await chat(prompt) else { return nil }
        return StructuredCallSummary.parse(from: reply, updatedAt: Date())
    }

    private func stopSessions() {
        sessions.forEach { $0.close() }
        sessions = []
        interpreterPair = nil
        outboundSession = nil
        audio?.onRemoteAudio = nil
        audio?.onLocalAudio = nil
        audio?.onLocalAudio24k = nil
        audio?.onAssistantBacklogDropped = nil
        audio?.setDirectMicPassthrough(false)
        directMicPassthrough = false
        for key in Array(drafts.keys) { closeDraft(key) }
        phase = ""
        isSimultaneousActive = false
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

    // MARK: Captions (symmetric pairing for `.them` and `.me`)

    private var drafts: [String: UUID] = [:]

    private func index(of id: UUID?) -> Int? {
        guard let id else { return nil }
        return captions.lastIndex { $0.id == id }
    }

    /// Finds a recent bubble for the same speaker missing either its primary or paired field.
    private func recent(
        _ speaker: AICaption.Speaker,
        missingChinese: Bool = false,
        missingTranslation: Bool = false
    ) -> Int? {
        guard let i = captions.lastIndex(where: { $0.speaker == speaker }),
              i >= captions.count - 2 else { return nil }
        if missingChinese {
            return (captions[i].chinese ?? "").isEmpty ? i : nil
        }
        if missingTranslation {
            return (captions[i].translation ?? "").isEmpty ? i : nil
        }
        return captions[i].text.isEmpty ? i : nil
    }

    /// Joins streaming transcript deltas without gluing adjacent ASCII words ("confirm" + "the").
    private static func joinDelta(_ existing: String, _ delta: String) -> String {
        guard !existing.isEmpty, !delta.isEmpty,
              let last = existing.unicodeScalars.last,
              let first = delta.unicodeScalars.first else {
            return existing + delta
        }
        let isASCIITail = last.isASCII && (CharacterSet.alphanumerics.contains(last) || ".!?,;:".unicodeScalars.contains(last))
        let isASCIIHead = first.isASCII && CharacterSet.alphanumerics.contains(first)
        if isASCIITail && isASCIIHead && !(last == "." && CharacterSet.decimalDigits.contains(first)) {
            return existing + " " + delta
        }
        return existing + delta
    }

    private func draft(
        _ key: String,
        _ speaker: AICaption.Speaker,
        chinese: Bool = false,
        outboundTranslation: Bool = false,
        _ delta: String
    ) {
        guard mode != .off else { return }
        if let i = index(of: drafts[key]) {
            if chinese {
                captions[i].chinese = Self.joinDelta(captions[i].chinese ?? "", delta)
            } else if outboundTranslation {
                captions[i].translation = Self.joinDelta(captions[i].translation ?? "", delta)
            } else {
                captions[i].text = Self.joinDelta(captions[i].text, delta)
            }
            return
        }
        let target: Int
        if chinese, let i = index(of: drafts["in.heard"]) ?? recent(speaker, missingChinese: true) {
            target = i
            captions[i].chinese = Self.joinDelta(captions[i].chinese ?? "", delta)
        } else if outboundTranslation, let i = index(of: drafts["out.heard"]) ?? recent(speaker, missingTranslation: true) {
            target = i
            captions[i].translation = Self.joinDelta(captions[i].translation ?? "", delta)
        } else if !chinese && !outboundTranslation, let i = recent(speaker) {
            target = i
            captions[i].text = Self.joinDelta(captions[i].text, delta)
        } else {
            let item: AICaption
            if chinese {
                item = AICaption(speaker: speaker, text: "", chinese: delta)
            } else if outboundTranslation {
                item = AICaption(speaker: speaker, text: "", translation: delta)
            } else {
                item = AICaption(speaker: speaker, text: delta)
            }
            captions.append(item)
            if captions.count > 300 { captions.removeFirst(captions.count - 300) }
            target = captions.count - 1
        }
        captions[target].live = true
        drafts[key] = captions[target].id
    }

    private func settle(
        _ key: String,
        _ speaker: AICaption.Speaker,
        chinese: Bool = false,
        outboundTranslation: Bool = false,
        _ text: String,
        translate: Bool = false
    ) {
        if let i = index(of: drafts.removeValue(forKey: key)) {
            if chinese {
                captions[i].chinese = text
            } else if outboundTranslation {
                captions[i].translation = text
            } else {
                captions[i].text = text
            }
            captions[i].live = drafts.values.contains(captions[i].id)
            if translate, !chinese, !outboundTranslation, !looksChinese(text) {
                translateCaption(captions[i].id, text)
            }
        } else if chinese {
            attachChinese(text, to: speaker)
        } else if outboundTranslation {
            attachTranslation(text, to: speaker)
        } else {
            add(speaker, text, translate: translate)
        }

        // Trigger rolling live summary when a meaningful side completes.
        if speaker != .note, (chinese || key == "out.heard" || key == "ag.heard" || key == "ag.said") {
            maybeTriggerRollingSummary()
        }
    }

    private func closeDraft(_ key: String) {
        guard let i = index(of: drafts.removeValue(forKey: key)) else { return }
        captions[i].live = drafts.values.contains(captions[i].id)
    }

    private func translateCaption(_ id: UUID, _ text: String) {
        Task {
            if let zh = try? await Self.chat("把下面这句电话里的话翻译成简体中文，只输出译文：\n\(text)") {
                if let i = captions.firstIndex(where: { $0.id == id }) { captions[i].chinese = zh }
            }
        }
    }

    private func add(_ speaker: AICaption.Speaker, _ text: String, translate: Bool = false) {
        if (speaker == .them || speaker == .me), mode == .translate,
           let i = captions.lastIndex(where: { $0.speaker == speaker }),
           i >= captions.count - 2,
           captions[i].text.isEmpty {
            captions[i].text = text
            return
        }
        captions.append(AICaption(speaker: speaker, text: text))
        if captions.count > 300 { captions.removeFirst(captions.count - 300) }
        guard translate, !looksChinese(text) else { return }
        translateCaption(captions.last!.id, text)
    }

    private func attachChinese(_ zh: String, to speaker: AICaption.Speaker) {
        if let i = captions.lastIndex(where: { $0.speaker == speaker && ($0.chinese ?? "").isEmpty }),
           i >= captions.count - 3 {
            captions[i].chinese = zh
        } else {
            captions.append(AICaption(speaker: speaker, text: "", chinese: zh))
        }
    }

    private func attachTranslation(_ tr: String, to speaker: AICaption.Speaker) {
        if let i = captions.lastIndex(where: { $0.speaker == speaker && ($0.translation ?? "").isEmpty }),
           i >= captions.count - 3 {
            captions[i].translation = tr
        } else {
            captions.append(AICaption(speaker: speaker, text: "", translation: tr))
        }
    }

    private func looksChinese(_ s: String) -> Bool {
        let han = s.unicodeScalars.filter { (0x4E00 ... 0x9FFF).contains($0.value) }.count
        return han * 3 >= s.count
    }

    // MARK: Text model helper (shared for live summary, final summary, and history regen)

    static func chat(_ prompt: String) async throws -> String {
        let settings = AICallSettings.shared
        if settings.usesGemini {
            return try await GeminiLiveSession.generate(
                key: settings.geminiKey,
                model: settings.geminiTextModel,
                prompt: prompt
            )
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

    // MARK: Connection check

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
                if enough { finish("连接正常，实时语音可用") }
            }
            session.onSaid = { _ in
                lock.lock(); let got = bytes; lock.unlock()
                finish(got > 0 ? "连接正常，实时语音可用" : "实时语音连上了，但没收到声音")
            }
            session.onError = { e in finish("Key 正常，但实时语音不行：\(e)") }
            connect(session, instructions: "Reply with one short word.", interrupt: true, transcribe: false)
            session.requestResponse("Say: hello.")
            DispatchQueue.global().asyncAfter(deadline: .now() + 20) {
                finish("实时语音 20 秒没回应（网络慢或模型 \(modelName) 不可用）")
            }
        }
    }

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
        guard s.isConfigured else { return "还没填 API Key" }
        if s.usesSimultaneous {
            let liveResult = await TranslateStreamSession.testConnection(base: s.trimmedBase, key: s.apiKey)
            switch liveResult {
            case .ok(let ms):
                return "同声传译通道正常（握手 \(ms) ms），可以直接使用"
            case .fail(let err):
                let fallback = await realtimeVoiceCheck()
                return "同传通道不可用（\(err)）；备用逐句模式：\(fallback)"
            }
        }
        guard let url = URL(string: s.trimmedBase + "/v1/models/\(s.realtimeModel)") else {
            return "AI 地址无效"
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

/// Remembers until when AI speech is coming out of the speaker (for non-headset echo protection).
final class SpeakerGate: @unchecked Sendable {
    private let lock = NSLock()
    private var until = Date.distantPast

    func extend(by seconds: Double) {
        lock.lock(); defer { lock.unlock() }
        until = max(until, Date()).addingTimeInterval(seconds)
    }

    /// Marks "still sounding now" (0.3 s echo tail follows).
    func hold() {
        lock.lock(); defer { lock.unlock() }
        until = max(until, Date())
    }

    var isSpeaking: Bool {
        lock.lock(); defer { lock.unlock() }
        return Date() < until.addingTimeInterval(0.3)
    }
}

/// Event names (no audio, no keys) for diagnosing AI calls, with 5 MB auto-rotation.
enum AICallLog {
    private static let queue = DispatchQueue(label: "djonehub.ai.log")
    private static let maxBytes: UInt64 = 5 * 1024 * 1024
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
            rotateIfNeeded()
            let data = Data("\(stamp) \(line)\n".utf8)
            if let h = try? FileHandle(forWritingTo: url) {
                h.seekToEndOfFile(); h.write(data); try? h.close()
            } else {
                try? data.write(to: url)
            }
        }
    }

    private static func rotateIfNeeded() {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: url.path),
              let size = attrs[.size] as? UInt64,
              size >= maxBytes else { return }
        let backup = url.deletingPathExtension().appendingPathExtension("log.1")
        try? FileManager.default.removeItem(at: backup)
        try? FileManager.default.moveItem(at: url, to: backup)
    }
}

/// 24 kHz -> 8 kHz for the phone line with a proper low-pass first, so the AI's
/// voice comes through clean instead of buzzy.
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
