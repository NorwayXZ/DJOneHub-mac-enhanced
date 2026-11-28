import Foundation

/// OpenAI's simultaneous interpreter (gpt-realtime-translate): audio streams
/// in continuously and the translation is spoken while the person is still
/// talking — no waiting for them to finish a sentence.
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

    private var task: URLSessionWebSocketTask?
    private let queue = DispatchQueue(label: "djonehub.ai.translate")
    private var pending = Data()
    private var closed = false
    private var ready = false
    private var leftover = Data()
    private var language = "en"
    // The interpreter never says "sentence done"; a short pause closes a caption.
    private var heardText = ""
    private var saidText = ""
    private var heardGen = 0
    private var saidGen = 0
    private var firstAudioLogged = false
    private static let captionPause: TimeInterval = 1.3

    func connect(base: String, key: String, language: String, transcribe: Bool) {
        let wsBase = base.replacingOccurrences(of: "https://", with: "wss://")
            .replacingOccurrences(of: "http://", with: "ws://")
        guard let url = URL(string: "\(wsBase)/v1/realtime/translations?model=gpt-realtime-translate") else {
            onError?("AI 地址无效")
            return
        }
        self.language = language
        var request = URLRequest(url: url)
        request.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        let task = URLSession.shared.webSocketTask(with: request)
        self.task = task
        task.resume()
        var input: [String: Any] = [:]
        if transcribe { input["transcription"] = ["model": "gpt-realtime-whisper"] }
        send(["type": "session.update", "session": [
            "audio": ["input": input, "output": ["language": language]] as [String: Any],
        ]])
        AICallLog.write("\(tag) translate: connecting → \(language)")
        receive()
        queue.asyncAfter(deadline: .now() + 6) { [weak self] in
            guard let self, !self.closed, !self.ready else { return }
            AICallLog.write("\(self.tag) translate: no session.updated after 6s, going ahead")
            self.ready = true
        }
    }

    /// Changes the language the translation is spoken in.
    func setLanguage(_ code: String) {
        queue.async { [weak self] in
            guard let self, !self.closed, code != self.language else { return }
            self.language = code
            AICallLog.write("\(self.tag) translate: now → \(code)")
            self.send(["type": "session.update",
                       "session": ["audio": ["output": ["language": code]]] as [String: Any]])
        }
    }

    /// 8 kHz call/mic audio, sent every ~100 ms. Silence must keep flowing too.
    func appendAudio8k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.pending.append(pcm)
            guard self.pending.count >= 1_600 else { return }
            let chunk = PCM.up3(self.pending)
            self.pending.removeAll(keepingCapacity: true)
            self.send(["type": "session.input_audio_buffer.append", "audio": chunk.base64EncodedString()])
        }
    }

    func requestResponse(_ extra: String?) {}
    func updateInstructions(_ text: String) {}

    func close() {
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            self.closed = true
            self.send(["type": "session.close"])
            let task = self.task
            self.task = nil
            DispatchQueue.global().asyncAfter(deadline: .now() + 0.5) {
                task?.cancel(with: .normalClosure, reason: nil)
            }
        }
    }

    // MARK: -

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
        case "session.created", "session.updated":
            ready = true
        case "session.output_audio.delta":
            guard let b64 = event["delta"] as? String, var pcm = Data(base64Encoded: b64) else { return }
            if !firstAudioLogged { firstAudioLogged = true; AICallLog.write("\(tag) first audio") }
            if !leftover.isEmpty { pcm = leftover + pcm; leftover.removeAll() }
            if pcm.count % 2 == 1 { leftover = pcm.suffix(1); pcm.removeLast() }
            onAudio?(pcm)
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
