import Foundation

/// OpenAI's simultaneous interpreter (`gpt-realtime-translate`): audio streams
/// in continuously at 24 kHz PCM16 and translated speech + transcript deltas
/// arrive while the speaker is still talking.
final class TranslateStreamSession: NSObject, VoiceAISession, @unchecked Sendable {
    var onAudio: ((Data) -> Void)?
    var onHeard: ((String) -> Void)?
    var onSaid: ((String) -> Void)?
    var onSpeechStarted: (() -> Void)?
    var onSpeechStopped: (() -> Void)?
    var tag = ""
    var onHeardDelta: ((String) -> Void)?
    var onSaidDelta: ((String) -> Void)?
    var onError: ((String) -> Void)?
    /// Fires when `session.updated` confirms target language and noise reduction.
    var onReady: (() -> Void)?

    private var task: URLSessionWebSocketTask?
    private let queue = DispatchQueue(label: "djonehub.ai.translate")
    private var pending8k = Data()
    private var pending24k = Data()
    private var closed = false
    private var updateSent = false
    private var ready = false
    private var leftover = Data()
    private var language = "en"
    private var noiseReduction: String? = "far_field"
    private var transcribe = true
    // The interpreter streams continuously without turn boundaries; a brief
    // pause closes the current caption bubble cleanly.
    private var heardText = ""
    private var saidText = ""
    private var heardGen = 0
    private var saidGen = 0
    private var connectedAt = Date()
    private var firstAudioLogged = false
    private var firstSpeechAudioLogged = false
    // `gpt-realtime-translate` emits 400 ms silent PCM packets continuously
    // even when nobody is speaking. Drop idle silence so it neither locks the
    // speaker echo-gate nor builds artificial latency in the phone uplink queue.
    private var speechHangoverChunks = 0
    private static let speechRMSThreshold = 0.004
    private static let captionPause: TimeInterval = 1.2
    /// 60 ms at 24 kHz mono PCM16 (lower latency than 100 ms, still compact).
    private static let chunkBytes24k = 2_880
    /// 60 ms at 8 kHz mono PCM16.
    private static let chunkBytes8k = 960

    func connect(
        base: String,
        key: String,
        language: String,
        transcribe: Bool,
        noiseReduction: String? = "far_field"
    ) {
        let wsBase = base.replacingOccurrences(of: "https://", with: "wss://")
            .replacingOccurrences(of: "http://", with: "ws://")
        guard let url = URL(string: "\(wsBase)/v1/realtime/translations?model=gpt-realtime-translate") else {
            onError?("AI 地址无效")
            return
        }
        queue.async { [weak self] in
            guard let self else { return }
            self.language = language
            self.transcribe = transcribe
            self.noiseReduction = noiseReduction
            self.connectedAt = Date()
            var request = URLRequest(url: url)
            request.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
            let task = URLSession.shared.webSocketTask(with: request)
            self.task = task
            task.resume()
            AICallLog.write("\(self.tag) translate: connecting → \(language) nr=\(noiseReduction ?? "none")")
            self.receive()
            // Wait for session.created before sending session.update, with a
            // relay fallback so custom proxies that omit session.created still work.
            self.queue.asyncAfter(deadline: .now() + 2.5) { [weak self] in
                guard let self, !self.closed, !self.updateSent else { return }
                self.sendSessionUpdate()
            }
            self.queue.asyncAfter(deadline: .now() + 6.0) { [weak self] in
                guard let self, !self.closed, !self.ready else { return }
                AICallLog.write("\(self.tag) translate: no session.updated after 6s, proceeding")
                self.ready = true
                self.onReady?()
            }
        }
    }

    /// Changes the output language mid-call.
    func setLanguage(_ code: String) {
        queue.async { [weak self] in
            guard let self, !self.closed, code != self.language else { return }
            self.language = code
            AICallLog.write("\(self.tag) translate: retarget → \(code)")
            if self.updateSent {
                self.send([
                    "type": "session.update",
                    "session": ["audio": ["output": ["language": code]]] as [String: Any],
                ])
            }
        }
    }

    /// Updates input noise reduction mid-call ("near_field" for headset, "far_field" for speakers).
    func setNoiseReduction(_ type: String?) {
        queue.async { [weak self] in
            guard let self, !self.closed, type != self.noiseReduction else { return }
            self.noiseReduction = type
            AICallLog.write("\(self.tag) translate: nr → \(type ?? "none")")
            guard self.updateSent else { return }
            let nrValue: Any = type.map { ["type": $0] as [String: Any] } ?? NSNull()
            self.send([
                "type": "session.update",
                "session": ["audio": ["input": ["noise_reduction": nrValue]]] as [String: Any],
            ])
        }
    }

    /// 8 kHz phone-line audio, upsampled to 24 kHz in 60 ms packets.
    func appendAudio8k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending8k.append(pcm)
            guard self.pending8k.count >= Self.chunkBytes8k else { return }
            let chunk = PCM.up3(self.pending8k)
            self.pending8k.removeAll(keepingCapacity: true)
            self.send(["type": "session.input_audio_buffer.append", "audio": chunk.base64EncodedString()])
        }
    }

    /// Native 24 kHz microphone audio, sent directly in 60 ms packets without 8 kHz loss.
    func appendAudio24k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending24k.append(pcm)
            guard self.pending24k.count >= Self.chunkBytes24k else { return }
            let chunk = self.pending24k
            self.pending24k.removeAll(keepingCapacity: true)
            self.send(["type": "session.input_audio_buffer.append", "audio": chunk.base64EncodedString()])
        }
    }

    func requestResponse(_ extra: String?) {}
    func updateInstructions(_ text: String) {}

    func close() {
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            self.closed = true
            self.flushDraftsNow()
            self.send(["type": "session.close"])
            let task = self.task
            self.task = nil
            DispatchQueue.global().asyncAfter(deadline: .now() + 0.5) {
                task?.cancel(with: .normalClosure, reason: nil)
            }
        }
    }

    enum TestOutcome: Sendable {
        case ok(Int)
        case fail(String)
    }

    /// Verifies that the configured OpenAI key and endpoint can open a
    /// `gpt-realtime-translate` session and configure a target language.
    static func testConnection(base: String, key: String, language: String = "en") async -> TestOutcome {
        let wsBase = base.replacingOccurrences(of: "https://", with: "wss://")
            .replacingOccurrences(of: "http://", with: "ws://")
        guard let url = URL(string: "\(wsBase)/v1/realtime/translations?model=gpt-realtime-translate") else {
            return .fail("AI 地址无效")
        }
        let started = Date()
        return await withCheckedContinuation { cont in
            let lock = NSLock()
            var done = false
            let session = TranslateStreamSession()
            session.tag = "test"
            func finish(_ r: TestOutcome) {
                lock.lock(); defer { lock.unlock() }
                guard !done else { return }
                done = true
                session.close()
                cont.resume(returning: r)
            }
            session.onReady = {
                let ms = Int(Date().timeIntervalSince(started) * 1000)
                finish(.ok(ms))
            }
            session.onError = { msg in finish(.fail(msg)) }
            session.connect(base: base, key: key, language: language, transcribe: true, noiseReduction: "near_field")
            DispatchQueue.global().asyncAfter(deadline: .now() + 12) {
                finish(.fail("同传接口 12 秒无响应（\(url.host ?? "")）"))
            }
        }
    }

    // MARK: - Private

    private func sendSessionUpdate() {
        updateSent = true
        var input: [String: Any] = [:]
        if transcribe { input["transcription"] = ["model": "gpt-realtime-whisper"] }
        if let nr = noiseReduction { input["noise_reduction"] = ["type": nr] }
        send([
            "type": "session.update",
            "session": [
                "audio": [
                    "input": input,
                    "output": ["language": language],
                ] as [String: Any],
            ],
        ])
    }

    private func flushDraftsNow() {
        heardGen += 1
        saidGen += 1
        let h = heardText.trimmingCharacters(in: .whitespacesAndNewlines)
        heardText = ""
        if !h.isEmpty { onHeard?(h) }
        let s = saidText.trimmingCharacters(in: .whitespacesAndNewlines)
        saidText = ""
        if !s.isEmpty { onSaid?(s) }
    }

    private func send(_ event: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: event),
              let text = String(data: data, encoding: .utf8) else { return }
        task?.send(.string(text)) { [weak self] error in
            if let error, let self, !self.closed {
                self.onError?("同传连接断开：\(error.localizedDescription)")
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
                AICallLog.write("\(self.tag) translate: closed \(error.localizedDescription) \(reason)")
                self.onError?("同传连接失败：\(reason.isEmpty ? error.localizedDescription : reason)")
            case .success(let message):
                switch message {
                case .string(let text): self.queue.async { self.handle(text) }
                case .data(let data):
                    let text = String(decoding: data, as: UTF8.self)
                    self.queue.async { self.handle(text) }
                @unknown default: break
                }
                self.receive()
            }
        }
    }

    /// Runs on `queue`.
    private func handle(_ text: String) {
        guard let data = text.data(using: .utf8),
              let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = event["type"] as? String else { return }
        if !type.hasSuffix(".delta") {
            AICallLog.write("\(tag) " + (type == "error" ? "error \(text.prefix(300))" : type))
        }
        switch type {
        case "session.created":
            if !updateSent { sendSessionUpdate() }
        case "session.updated":
            if !ready {
                ready = true
                let ms = Int(Date().timeIntervalSince(connectedAt) * 1000)
                AICallLog.write("\(tag) translate: ready in \(ms) ms (→ \(language))")
                onReady?()
            }
        case "session.output_audio.delta":
            guard let b64 = event["delta"] as? String, var pcm = Data(base64Encoded: b64) else { return }
            if !leftover.isEmpty { pcm = leftover + pcm; leftover.removeAll() }
            if pcm.count % 2 == 1 { leftover = pcm.suffix(1); pcm.removeLast() }
            if !firstAudioLogged {
                firstAudioLogged = true
                let ms = Int(Date().timeIntervalSince(connectedAt) * 1000)
                AICallLog.write("\(tag) translate: stream alive at +\(ms) ms")
            }
            let rms = PCM.rms16(pcm)
            if rms >= Self.speechRMSThreshold {
                speechHangoverChunks = 1
                if !firstSpeechAudioLogged {
                    firstSpeechAudioLogged = true
                    let ms = Int(Date().timeIntervalSince(connectedAt) * 1000)
                    AICallLog.write(String(format: "%@ translate: first SPEECH audio at +%d ms rms=%.4f", tag, ms, rms))
                }
                onAudio?(pcm)
            } else if speechHangoverChunks > 0 {
                // Keep one brief trailing tail (150 ms) after real speech so
                // phrase endings sound natural without queuing idle silence.
                speechHangoverChunks -= 1
                let tailBytes = min(pcm.count, 7_200)
                if tailBytes >= 2 { onAudio?(pcm.prefix(tailBytes - (tailBytes % 2))) }
            }
        case "session.input_transcript.delta":
            guard let d = event["delta"] as? String, !d.isEmpty else { return }
            if heardText.isEmpty { onSpeechStarted?() }
            heardText += d
            onHeardDelta?(d)
            heardGen += 1
            let gen = heardGen
            queue.asyncAfter(deadline: .now() + Self.captionPause) { [weak self] in
                guard let self, self.heardGen == gen else { return }
                let t = self.heardText.trimmingCharacters(in: .whitespacesAndNewlines)
                self.heardText = ""
                if !t.isEmpty { self.onHeard?(t) }
            }
        case "session.output_transcript.delta":
            guard let d = event["delta"] as? String, !d.isEmpty else { return }
            saidText += d
            onSaidDelta?(d)
            saidGen += 1
            let gen = saidGen
            queue.asyncAfter(deadline: .now() + Self.captionPause) { [weak self] in
                guard let self, self.saidGen == gen else { return }
                let t = self.saidText.trimmingCharacters(in: .whitespacesAndNewlines)
                self.saidText = ""
                if !t.isEmpty { self.onSaid?(t) }
            }
        case "error":
            let err = event["error"] as? [String: Any]
            let msg = (err?["message"] as? String) ?? "未知错误"
            let lower = msg.lowercased()
            if ready && !lower.contains("model") && !lower.contains("language") {
                AICallLog.write("\(tag) translate: non-fatal error \(msg)")
            } else {
                onError?("同传出错：\(msg)")
            }
        default:
            break
        }
    }
}
