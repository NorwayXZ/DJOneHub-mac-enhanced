import Foundation

/// What the call AI needs from a live speech-to-speech engine, so OpenAI
/// Realtime and Gemini Live can be swapped in settings.
protocol VoiceAISession: AnyObject {
    var onAudio: ((Data) -> Void)? { get set }          // 24 kHz PCM16
    var onHeard: ((String) -> Void)? { get set }        // what came in
    var onSaid: ((String) -> Void)? { get set }         // what the AI said
    var onSpeechStarted: (() -> Void)? { get set }
    /// Word-by-word pieces while someone is still talking (for live captions).
    var onHeardDelta: ((String) -> Void)? { get set }
    var onSaidDelta: ((String) -> Void)? { get set }
    var onError: ((String) -> Void)? { get set }
    func appendAudio8k(_ pcm: Data)
    func requestResponse(_ extra: String?)
    func updateInstructions(_ text: String)
    func close()
}

extension RealtimeVoiceSession: VoiceAISession {}

/// One Gemini Live conversation over its WebSocket (BidiGenerateContent).
/// Audio goes in at 16 kHz and comes back at 24 kHz.
final class GeminiLiveSession: NSObject, VoiceAISession, @unchecked Sendable {
    var onAudio: ((Data) -> Void)?
    var onHeard: ((String) -> Void)?
    var onSaid: ((String) -> Void)?
    var onSpeechStarted: (() -> Void)?
    var onHeardDelta: ((String) -> Void)?
    var onSaidDelta: ((String) -> Void)?
    var onError: ((String) -> Void)?

    private var task: URLSessionWebSocketTask?
    private let queue = DispatchQueue(label: "djonehub.ai.gemini")
    private var pending = Data()
    private var closed = false
    private var ready = false
    private var queuedTurn: [String: Any]?
    private var queuedNotes: [[String: Any]] = []
    private var heardText = ""
    private var saidText = ""
    private var heardThisTurn = false
    private var leftover = Data()

    func connect(key: String, model: String, voice: String, instructions: String,
                 interrupt: Bool, eagerness: String) {
        var comps = URLComponents(string:
            "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent")!
        comps.queryItems = [URLQueryItem(name: "key", value: key)]
        guard let url = comps.url else { onError?("Gemini 地址无效"); return }
        let task = URLSession.shared.webSocketTask(with: url)
        self.task = task
        task.resume()
        let silence: Int
        switch eagerness {
        case "low": silence = 900
        case "medium": silence = 600
        default: silence = 350
        }
        let setup: [String: Any] = [
            "setup": [
                "model": model.hasPrefix("models/") ? model : "models/\(model)",
                "generationConfig": [
                    "responseModalities": ["AUDIO"],
                    "speechConfig": ["voiceConfig": ["prebuiltVoiceConfig": ["voiceName": voice]]],
                ] as [String: Any],
                "systemInstruction": ["parts": [["text": instructions]]],
                "inputAudioTranscription": [String: Any](),
                "outputAudioTranscription": [String: Any](),
                "realtimeInputConfig": [
                    "automaticActivityDetection": [
                        "startOfSpeechSensitivity": "START_SENSITIVITY_HIGH",
                        "endOfSpeechSensitivity": eagerness == "low" ? "END_SENSITIVITY_LOW" : "END_SENSITIVITY_HIGH",
                        "prefixPaddingMs": 80,
                        "silenceDurationMs": silence,
                    ] as [String: Any],
                    "activityHandling": interrupt ? "START_OF_ACTIVITY_INTERRUPTS" : "NO_INTERRUPTION",
                ] as [String: Any],
            ] as [String: Any],
        ]
        AICallLog.write("gemini: connecting \(model)")
        send(setup)
        receive()
        // Don't wait forever for setupComplete.
        queue.asyncAfter(deadline: .now() + 6) { [weak self] in
            guard let self, !self.closed, !self.ready else { return }
            AICallLog.write("gemini: no setupComplete after 6s, going ahead")
            self.becomeReady()
        }
    }

    func appendAudio8k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending.append(pcm)
            guard self.pending.count >= 1_600 else { return }
            let chunk = Self.up2(self.pending)
            self.pending.removeAll(keepingCapacity: true)
            self.send(["realtimeInput": ["audio": [
                "data": chunk.base64EncodedString(),
                "mimeType": "audio/pcm;rate=16000",
            ]]])
        }
    }

    func requestResponse(_ extra: String?) {
        let text = extra ?? "Please respond now."
        let turn: [String: Any] = ["clientContent": [
            "turns": [["role": "user", "parts": [["text": "(Note from the app, not the caller) " + text]]]],
            "turnComplete": true,
        ] as [String: Any]]
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            if self.ready { self.send(turn) } else { self.queuedTurn = turn }
        }
    }

    /// Gemini Live can't change its system instruction mid-session, so the
    /// change is added to the conversation as a note without asking for a reply.
    func updateInstructions(_ text: String) {
        let note: [String: Any] = ["clientContent": [
            "turns": [["role": "user", "parts": [["text": "(Updated instructions from the app — follow these from now on, don't reply to this note)\n" + text]]]],
            "turnComplete": false,
        ] as [String: Any]]
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            if self.ready { self.send(note) } else { self.queuedNotes.append(note) }
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

    // MARK: -

    private func becomeReady() {
        guard !ready else { return }
        ready = true
        queuedNotes.forEach(send)
        queuedNotes = []
        if let t = queuedTurn { queuedTurn = nil; send(t) }
    }

    private func send(_ event: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: event),
              let text = String(data: data, encoding: .utf8) else { return }
        task?.send(.string(text)) { [weak self] error in
            if let error, let self, !self.closed {
                self.onError?("Gemini 连接断开：\(error.localizedDescription)")
            }
        }
    }

    private func receive() {
        task?.receive { [weak self] result in
            guard let self, !self.closed else { return }
            switch result {
            case .failure(let error):
                self.closed = true
                let reason = self.task?.closeReason.flatMap { String(data: $0, encoding: .utf8) } ?? ""
                AICallLog.write("gemini: closed \(error.localizedDescription) \(reason)")
                self.onError?("Gemini 连接失败：\(reason.isEmpty ? error.localizedDescription : reason)")
            case .success(let message):
                switch message {
                case .string(let text): self.handle(Data(text.utf8))
                case .data(let data): self.handle(data)
                @unknown default: break
                }
                self.receive()
            }
        }
    }

    private func handle(_ data: Data) {
        guard let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        if event["setupComplete"] != nil {
            AICallLog.write("gemini: setupComplete")
            queue.async { [weak self] in self?.becomeReady() }
            return
        }
        if let goAway = event["goAway"] {
            AICallLog.write("gemini: goAway \(goAway)")
        }
        guard let content = event["serverContent"] as? [String: Any] else { return }

        if let input = (content["inputTranscription"] as? [String: Any])?["text"] as? String, !input.isEmpty {
            if !heardThisTurn {
                heardThisTurn = true
                onSpeechStarted?()
            }
            heardText += input
            onHeardDelta?(input)
        }
        if content["interrupted"] as? Bool == true {
            AICallLog.write("gemini: interrupted")
            onSpeechStarted?()
            flushSaid()
        }
        if let turn = content["modelTurn"] as? [String: Any],
           let parts = turn["parts"] as? [[String: Any]] {
            flushHeard()
            for part in parts {
                guard let inline = part["inlineData"] as? [String: Any],
                      let b64 = inline["data"] as? String,
                      var pcm = Data(base64Encoded: b64) else { continue }
                if !leftover.isEmpty { pcm = leftover + pcm; leftover.removeAll() }
                if pcm.count % 2 == 1 { leftover = pcm.suffix(1); pcm.removeLast() }
                onAudio?(pcm)
            }
        }
        if let output = (content["outputTranscription"] as? [String: Any])?["text"] as? String {
            saidText += output
            if !output.isEmpty { onSaidDelta?(output) }
        }
        if content["generationComplete"] as? Bool == true || content["turnComplete"] as? Bool == true {
            flushHeard()
            flushSaid()
        }
    }

    private func flushHeard() {
        let text = heardText.trimmingCharacters(in: .whitespacesAndNewlines)
        heardText = ""
        heardThisTurn = false
        if !text.isEmpty { onHeard?(text) }
    }

    private func flushSaid() {
        let text = saidText.trimmingCharacters(in: .whitespacesAndNewlines)
        saidText = ""
        if !text.isEmpty { onSaid?(text) }
    }

    /// 8 kHz -> 16 kHz, linear interpolation.
    static func up2(_ data: Data) -> Data {
        let n = data.count / 2
        guard n > 0 else { return Data() }
        var out = [Int16](repeating: 0, count: n * 2)
        data.withUnsafeBytes { raw in
            let s = raw.bindMemory(to: Int16.self)
            for i in 0 ..< n {
                let a = Int(Int16(littleEndian: s[i]))
                let b = i + 1 < n ? Int(Int16(littleEndian: s[i + 1])) : a
                out[i * 2] = Int16(a)
                out[i * 2 + 1] = Int16((a + b) / 2)
            }
        }
        return out.withUnsafeBytes { Data($0) }
    }

    /// Text model call for captions, language detection and summaries.
    static func generate(key: String, model: String, prompt: String) async throws -> String {
        guard let url = URL(string:
            "https://generativelanguage.googleapis.com/v1beta/models/\(model):generateContent") else {
            throw URLError(.badURL)
        }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.timeoutInterval = 30
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(key, forHTTPHeaderField: "x-goog-api-key")
        request.httpBody = try JSONSerialization.data(withJSONObject: [
            "contents": [["role": "user", "parts": [["text": prompt]]]],
        ])
        let (data, response) = try await URLSession.shared.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200,
              let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
              let candidates = json["candidates"] as? [[String: Any]],
              let content = candidates.first?["content"] as? [String: Any],
              let parts = content["parts"] as? [[String: Any]] else {
            throw URLError(.badServerResponse)
        }
        return parts.compactMap { $0["text"] as? String }.joined()
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
