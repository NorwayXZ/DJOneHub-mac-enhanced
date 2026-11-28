import Foundation

/// Ultra-fast 3-stage streaming translation pipeline:
///   1. Streaming STT: Deepgram Nova-3 (if key set) or OpenAI Realtime Transcription
///      (`gpt-4o-transcribe` with explicit source-language lock so Chinese is never
///      misheard as Japanese/Russian).
///   2. Streaming MT: `gpt-5.4-nano` (or custom Groq/Cerebras OpenAI-compatible fast API)
///      producing casual, idiomatic phone speech clause-by-clause.
///   3. Streaming Lifelike TTS: Cartesia Sonic, ElevenLabs Flash v2.5, or OpenAI
///      `gpt-4o-mini-tts` (24 kHz PCM16 streamed as bytes arrive).
final class FastCascadeSession: NSObject, VoiceAISession, @unchecked Sendable {
    struct Config: Sendable {
        var sourceLangName: String       // e.g. "Mandarin Chinese" or "English"
        var sourceLangCode: String?      // e.g. "zh" or "en" (nil = auto)
        var targetLangName: String       // e.g. "English" or "Simplified Chinese"
        var targetLangCode: String       // e.g. "en" or "zh"
        var ownerName: String
        var noiseReduction: String       // "near_field" or "far_field"

        // OpenAI base/key (used as zero-extra-key fallback for STT, MT, TTS)
        var openAIBase: String
        var openAIKey: String
        var openAIVoice: String

        // Optional Stage 1: Deepgram Nova-3
        var deepgramKey: String

        // Optional Stage 2: Fast MT override (e.g. Groq / Cerebras / gpt-5.4-nano)
        var mtBaseURL: String
        var mtKey: String
        var mtModel: String

        // Stage 3: TTS provider ("openai" | "cartesia" | "elevenlabs")
        var ttsProvider: String
        var cartesiaKey: String
        var cartesiaVoiceID: String
        var elevenLabsKey: String
        var elevenLabsVoiceID: String
    }

    var onAudio: ((Data) -> Void)?
    var onHeard: ((String) -> Void)?
    var onSaid: ((String) -> Void)?
    var onSpeechStarted: (() -> Void)?
    var onSpeechStopped: (() -> Void)?
    var tag = ""
    var onHeardDelta: ((String) -> Void)?
    var onSaidDelta: ((String) -> Void)?
    var onError: ((String) -> Void)?

    private var config: Config
    private let queue = DispatchQueue(label: "djonehub.ai.cascade")
    private var sttTask: URLSessionWebSocketTask?
    private var closed = false
    private var sttReady = false
    private var pending24k = Data()
    private var pending8k = Data()

    // Deepgram accumulator
    private var dgFinalBuffer = ""
    private var dgInterimActive = false
    // OpenAI STT accumulator
    private var oaiHeardDelta = ""
    // Rolling 4-turn context for natural pronoun/reference translation
    private var recentPairs: [(src: String, dst: String)] = []

    // Strict FIFO serial execution for MT -> TTS so audio never overlaps
    private let pipelineLock = NSLock()
    private var utteranceQueue: [String] = []
    private var pipelineRunning = false
    private var activeTask: Task<Void, Never>?

    /// 40 ms chunks at 24 kHz mono PCM16
    private static let chunk24k = 1_920
    /// 40 ms chunks at 8 kHz mono PCM16
    private static let chunk8k = 640

    init(config: Config) {
        self.config = config
        super.init()
    }

    func connect() {
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            if !self.config.deepgramKey.isEmpty {
                self.connectDeepgram()
            } else {
                self.connectOpenAISTT()
            }
        }
    }

    func setTargetLanguage(name: String, code: String) {
        queue.async { [weak self] in
            guard let self else { return }
            self.config.targetLangName = name
            self.config.targetLangCode = code
            AICallLog.write("\(self.tag) cascade: target → \(name) (\(code))")
        }
    }

    func appendAudio8k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.sttReady else { return }
            self.pending8k.append(pcm)
            guard self.pending8k.count >= Self.chunk8k else { return }
            let pcm24 = PCM.up3(self.pending8k)
            self.pending8k.removeAll(keepingCapacity: true)
            self.sendSTTAudio24k(pcm24)
        }
    }

    func appendAudio24k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.sttReady else { return }
            self.pending24k.append(pcm)
            guard self.pending24k.count >= Self.chunk24k else { return }
            let chunk = self.pending24k
            self.pending24k.removeAll(keepingCapacity: true)
            self.sendSTTAudio24k(chunk)
        }
    }

    func requestResponse(_ extra: String?) {}
    func updateInstructions(_ text: String) {}

    func close() {
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            self.closed = true
            self.sttReady = false
            self.sttTask?.cancel(with: .normalClosure, reason: nil)
            self.sttTask = nil
        }
        pipelineLock.lock()
        utteranceQueue.removeAll()
        activeTask?.cancel()
        activeTask = nil
        pipelineLock.unlock()
    }

    // MARK: - Stage 1A: Deepgram Nova-3 Streaming STT

    private var usesDeepgram: Bool { !config.deepgramKey.isEmpty }

    private func connectDeepgram() {
        let dgLang: String
        if let code = config.sourceLangCode {
            dgLang = code == "zh" ? "zh-CN" : code
        } else {
            dgLang = "multi"
        }
        var comps = URLComponents(string: "wss://api.deepgram.com/v1/listen")!
        comps.queryItems = [
            URLQueryItem(name: "model", value: "nova-3"),
            URLQueryItem(name: "encoding", value: "linear16"),
            URLQueryItem(name: "sample_rate", value: "24000"),
            URLQueryItem(name: "channels", value: "1"),
            URLQueryItem(name: "language", value: dgLang),
            URLQueryItem(name: "punctuate", value: "true"),
            URLQueryItem(name: "smart_format", value: "true"),
            URLQueryItem(name: "interim_results", value: "true"),
            URLQueryItem(name: "endpointing", value: "260"),
            URLQueryItem(name: "utterance_end_ms", value: "750"),
        ]
        guard let url = comps.url else { onError?("Deepgram 地址无效"); return }
        var req = URLRequest(url: url)
        req.setValue("Token \(config.deepgramKey)", forHTTPHeaderField: "Authorization")
        let task = URLSession.shared.webSocketTask(with: req)
        sttTask = task
        task.resume()
        sttReady = true
        AICallLog.write("\(tag) cascade: Deepgram Nova-3 connected (lang=\(dgLang))")
        receiveSTT()
    }

    // MARK: - Stage 1B: OpenAI Realtime Transcription (zero extra key)

    private func connectOpenAISTT() {
        let wsBase = config.openAIBase
            .replacingOccurrences(of: "https://", with: "wss://")
            .replacingOccurrences(of: "http://", with: "ws://")
        guard let url = URL(string: "\(wsBase)/v1/realtime?intent=transcription") else {
            onError?("AI 地址无效")
            return
        }
        var req = URLRequest(url: url)
        req.setValue("Bearer \(config.openAIKey)", forHTTPHeaderField: "Authorization")
        let task = URLSession.shared.webSocketTask(with: req)
        sttTask = task
        task.resume()
        AICallLog.write("\(tag) cascade: OpenAI transcription connecting (lang=\(config.sourceLangCode ?? "auto"))")
        receiveSTT()
        queue.asyncAfter(deadline: .now() + 2.5) { [weak self] in
            guard let self, !self.closed, !self.sttReady else { return }
            self.sendOpenAISTTConfig()
            self.sttReady = true
        }
    }

    private func sendOpenAISTTConfig() {
        var transcription: [String: Any] = ["model": "gpt-4o-transcribe"]
        if let code = config.sourceLangCode, !code.isEmpty {
            transcription["language"] = code
        }
        let session: [String: Any] = [
            "type": "transcription",
            "audio": [
                "input": [
                    "format": ["type": "audio/pcm", "rate": 24_000],
                    "transcription": transcription,
                    "noise_reduction": ["type": config.noiseReduction],
                    "turn_detection": [
                        "type": "server_vad",
                        "threshold": 0.5,
                        "prefix_padding_ms": 180,
                        "silence_duration_ms": 280,
                    ] as [String: Any],
                ] as [String: Any],
            ],
        ]
        sendSTTJSON(["type": "session.update", "session": session])
    }

    private func sendSTTAudio24k(_ pcm24: Data) {
        if usesDeepgram {
            sttTask?.send(.data(pcm24)) { _ in }
        } else {
            sendSTTJSON(["type": "input_audio_buffer.append", "audio": pcm24.base64EncodedString()])
        }
    }

    private func sendSTTJSON(_ obj: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: obj),
              let text = String(data: data, encoding: .utf8) else { return }
        sttTask?.send(.string(text)) { [weak self] err in
            if let err, let self, !self.closed {
                self.onError?("听写连接断开：\(err.localizedDescription)")
            }
        }
    }

    private func receiveSTT() {
        sttTask?.receive { [weak self] res in
            guard let self, !self.closed else { return }
            switch res {
            case .failure(let err):
                self.closed = true
                AICallLog.write("\(self.tag) cascade stt closed: \(err.localizedDescription)")
                self.onError?("听写连接失败：\(err.localizedDescription)")
            case .success(let msg):
                let txt: String?
                switch msg {
                case .string(let s): txt = s
                case .data(let d): txt = String(decoding: d, as: UTF8.self)
                @unknown default: txt = nil
                }
                if let txt { self.queue.async { self.handleSTTMessage(txt) } }
                self.receiveSTT()
            }
        }
    }

    private func handleSTTMessage(_ text: String) {
        guard let data = text.data(using: .utf8),
              let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = event["type"] as? String else { return }

        if usesDeepgram {
            handleDeepgramEvent(type, event: event)
        } else {
            handleOpenAISTTEvent(type, event: event)
        }
    }

    private func handleDeepgramEvent(_ type: String, event: [String: Any]) {
        switch type {
        case "Results":
            let ch = event["channel"] as? [String: Any]
            let alts = ch?["alternatives"] as? [[String: Any]]
            let transcript = (alts?.first?["transcript"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let isFinal = (event["is_final"] as? Bool) == true
            let speechFinal = (event["speech_final"] as? Bool) == true

            if !transcript.isEmpty {
                if !dgInterimActive {
                    dgInterimActive = true
                    onSpeechStarted?()
                }
                if isFinal {
                    dgFinalBuffer = dgFinalBuffer.isEmpty ? transcript : (dgFinalBuffer + " " + transcript)
                }
            }
            if speechFinal {
                commitDeepgramUtterance()
            }
        case "UtteranceEnd":
            commitDeepgramUtterance()
        default:
            break
        }
    }

    private func commitDeepgramUtterance() {
        let phrase = dgFinalBuffer.trimmingCharacters(in: .whitespacesAndNewlines)
        dgFinalBuffer = ""
        dgInterimActive = false
        guard !phrase.isEmpty else { return }
        onSpeechStopped?()
        onHeard?(phrase)
        enqueueUtterance(phrase)
    }

    private func handleOpenAISTTEvent(_ type: String, event: [String: Any]) {
        if !type.hasSuffix(".delta") {
            AICallLog.write("\(tag) cascade-stt \(type == "error" ? "error \(event)" : type)")
        }
        switch type {
        case "transcription_session.created", "session.created":
            sendOpenAISTTConfig()
        case "transcription_session.updated", "session.updated":
            sttReady = true
            AICallLog.write("\(tag) cascade: OpenAI STT ready")
        case "input_audio_buffer.speech_started":
            oaiHeardDelta = ""
            onSpeechStarted?()
        case "input_audio_buffer.speech_stopped":
            onSpeechStopped?()
        case "conversation.item.input_audio_transcription.delta":
            if let d = event["delta"] as? String, !d.isEmpty {
                oaiHeardDelta += d
                onHeardDelta?(d)
            }
        case "conversation.item.input_audio_transcription.completed":
            let t = ((event["transcript"] as? String) ?? oaiHeardDelta)
                .trimmingCharacters(in: .whitespacesAndNewlines)
            oaiHeardDelta = ""
            guard !t.isEmpty else { return }
            onHeard?(t)
            enqueueUtterance(t)
        case "error":
            let err = event["error"] as? [String: Any]
            let msg = (err?["message"] as? String) ?? "听写出错"
            if !sttReady { onError?("听写启动失败：\(msg)") }
        default:
            break
        }
    }

    // MARK: - Stage 2 (Streaming MT) + Stage 3 (Streaming Lifelike TTS)

    private func enqueueUtterance(_ text: String) {
        pipelineLock.lock()
        utteranceQueue.append(text)
        let shouldStart = !pipelineRunning
        if shouldStart { pipelineRunning = true }
        pipelineLock.unlock()
        guard shouldStart else { return }
        activeTask = Task { [weak self] in
            await self?.drainUtterances()
        }
    }

    private func popNextUtterance() -> String? {
        pipelineLock.lock()
        defer { pipelineLock.unlock() }
        if utteranceQueue.isEmpty {
            pipelineRunning = false
            return nil
        }
        return utteranceQueue.removeFirst()
    }

    private func drainUtterances() async {
        while !Task.isCancelled {
            guard let src = popNextUtterance() else { return }
            await translateAndSpeak(src)
        }
    }

    private func translateAndSpeak(_ sourceText: String) async {
        let cfg = queue.sync { self.config }
        let contextPairs = queue.sync { self.recentPairs }
        let t0 = Date()

        let ownerHint = cfg.ownerName.trimmingCharacters(in: .whitespaces).isEmpty
            ? ""
            : " Your client's name is \(cfg.ownerName); never misspell it."
        let systemPrompt = """
        You are a warm, natural, native-level phone interpreter translating \(cfg.sourceLangName) into spoken \(cfg.targetLangName).\(ownerHint)
        Rules:
        - Sound like a real person talking casually and politely on a phone call — NEVER translate stiffly word-for-word.
        - Drop filler words ("这个", "那个", "嗯") and self-corrections; convey the real intent smoothly.
        - Keep every number, price, date, time, address, and person/company name 100% exact.
        - Output ONLY the spoken \(cfg.targetLangName) translation. No quotes, no explanations, no prefixes.
        """

        var messages: [[String: String]] = [["role": "system", "content": systemPrompt]]
        for pair in contextPairs.suffix(4) {
            messages.append(["role": "user", "content": pair.src])
            messages.append(["role": "assistant", "content": pair.dst])
        }
        messages.append(["role": "user", "content": sourceText])

        let mtBase = cfg.mtBaseURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? cfg.openAIBase : cfg.mtBaseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        let mtKey = cfg.mtKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? cfg.openAIKey : cfg.mtKey.trimmingCharacters(in: .whitespacesAndNewlines)
        let mtModel = cfg.mtModel.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? "gpt-5.4-nano" : cfg.mtModel.trimmingCharacters(in: .whitespacesAndNewlines)

        let cleanBase = mtBase.hasSuffix("/v1") ? String(mtBase.dropLast(3)) : mtBase.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        guard let url = URL(string: "\(cleanBase)/v1/chat/completions") else { return }

        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 20
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(mtKey)", forHTTPHeaderField: "Authorization")
        req.httpBody = try? JSONSerialization.data(withJSONObject: [
            "model": mtModel,
            "stream": true,
            "messages": messages,
        ])

        var fullTranslation = ""
        var clauseBuf = ""
        var firstTokenLogged = false
        var firstAudioLogged = false

        // Launch TTS tasks in strict sequential order per clause
        var ttsChain: Task<Void, Never>? = nil
        func queueClauseForTTS(_ clause: String) {
            let trimmed = clause.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty else { return }
            let prev = ttsChain
            ttsChain = Task { [weak self] in
                await prev?.value
                guard !Task.isCancelled, let self else { return }
                await self.streamTTS(trimmed, cfg: cfg, startedAt: t0, firstAudioLogged: &firstAudioLogged)
            }
        }

        do {
            let (bytes, response) = try await URLSession.shared.bytes(for: req)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else {
                let code = (response as? HTTPURLResponse)?.statusCode ?? 0
                AICallLog.write("\(tag) cascade MT HTTP \(code)")
                return
            }
            for try await line in bytes.lines {
                if Task.isCancelled { break }
                guard line.hasPrefix("data:") else { continue }
                let payload = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                if payload == "[DONE]" { break }
                guard let data = payload.data(using: .utf8),
                      let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let choices = json["choices"] as? [[String: Any]],
                      let delta = choices.first?["delta"] as? [String: Any],
                      let token = delta["content"] as? String, !token.isEmpty else { continue }

                if !firstTokenLogged {
                    firstTokenLogged = true
                    let ms = Int(Date().timeIntervalSince(t0) * 1000)
                    AICallLog.write("\(tag) cascade MT first token +\(ms) ms (\(mtModel))")
                }
                fullTranslation += token
                clauseBuf += token
                onSaidDelta?(token)

                if shouldFlushClause(clauseBuf) {
                    queueClauseForTTS(clauseBuf)
                    clauseBuf = ""
                }
            }
        } catch {
            AICallLog.write("\(tag) cascade MT error: \(error.localizedDescription)")
        }

        if !clauseBuf.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            queueClauseForTTS(clauseBuf)
        }

        let finalText = fullTranslation.trimmingCharacters(in: .whitespacesAndNewlines)
        if !finalText.isEmpty {
            onSaid?(finalText)
            queue.async { [weak self] in
                guard let self else { return }
                self.recentPairs.append((src: sourceText, dst: finalText))
                if self.recentPairs.count > 6 { self.recentPairs.removeFirst() }
            }
        }
        await ttsChain?.value
    }

    private func shouldFlushClause(_ buf: String) -> Bool {
        let trimmed = buf.trimmingCharacters(in: .whitespaces)
        guard let last = trimmed.last else { return false }
        if ".?!。？！;；\n".contains(last), trimmed.count >= 6 { return true }
        if ",，".contains(last), trimmed.count >= 28 { return true }
        return false
    }

    // MARK: - Stage 3: Streaming Lifelike TTS (24 kHz PCM16)

    private func streamTTS(_ text: String, cfg: Config, startedAt: Date, firstAudioLogged: inout Bool) async {
        switch cfg.ttsProvider {
        case "cartesia" where !cfg.cartesiaKey.isEmpty:
            await streamCartesiaTTS(text, cfg: cfg, startedAt: startedAt, firstAudioLogged: &firstAudioLogged)
        case "elevenlabs" where !cfg.elevenLabsKey.isEmpty:
            await streamElevenLabsTTS(text, cfg: cfg, startedAt: startedAt, firstAudioLogged: &firstAudioLogged)
        default:
            await streamOpenAITTS(text, cfg: cfg, startedAt: startedAt, firstAudioLogged: &firstAudioLogged)
        }
    }

    private func streamCartesiaTTS(_ text: String, cfg: Config, startedAt: Date, firstAudioLogged: inout Bool) async {
        guard let url = URL(string: "https://api.cartesia.ai/tts/bytes") else { return }
        // Default Cartesia warm conversational voice if user left Voice ID blank
        let voiceID = cfg.cartesiaVoiceID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? "a0e99841-438c-4a64-b679-ae501e7d6091"
            : cfg.cartesiaVoiceID.trimmingCharacters(in: .whitespacesAndNewlines)
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 20
        req.setValue(cfg.cartesiaKey, forHTTPHeaderField: "X-API-Key")
        req.setValue("2025-04-16", forHTTPHeaderField: "Cartesia-Version")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONSerialization.data(withJSONObject: [
            "model_id": "sonic-2",
            "transcript": text,
            "voice": ["mode": "id", "id": voiceID],
            "language": cfg.targetLangCode,
            "output_format": [
                "container": "raw",
                "encoding": "pcm_s16le",
                "sample_rate": 24_000,
            ] as [String: Any],
        ])
        await streamRawPCM24k(request: req, provider: "cartesia", startedAt: startedAt, firstAudioLogged: &firstAudioLogged)
    }

    private func streamElevenLabsTTS(_ text: String, cfg: Config, startedAt: Date, firstAudioLogged: inout Bool) async {
        // Default ElevenLabs natural voice ("Rachel" / "Adam") if user left Voice ID blank
        let voiceID = cfg.elevenLabsVoiceID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            ? "21m00Tcm4TlvDq8ikWAM"
            : cfg.elevenLabsVoiceID.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: "https://api.elevenlabs.io/v1/text-to-speech/\(voiceID)/stream?output_format=pcm_24000&optimize_streaming_latency=4") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 20
        req.setValue(cfg.elevenLabsKey, forHTTPHeaderField: "xi-api-key")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONSerialization.data(withJSONObject: [
            "text": text,
            "model_id": "eleven_flash_v2_5",
        ])
        await streamRawPCM24k(request: req, provider: "elevenlabs", startedAt: startedAt, firstAudioLogged: &firstAudioLogged)
    }

    private func streamOpenAITTS(_ text: String, cfg: Config, startedAt: Date, firstAudioLogged: inout Bool) async {
        guard let url = URL(string: cfg.openAIBase + "/v1/audio/speech") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 20
        req.setValue("Bearer \(cfg.openAIKey)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONSerialization.data(withJSONObject: [
            "model": "gpt-4o-mini-tts",
            "voice": cfg.openAIVoice,
            "input": text,
            "response_format": "pcm",
            "instructions": "Speak naturally, warmly, and casually like a real person on a live phone call. Relaxed conversational pace, natural intonation, never robotic.",
        ])
        await streamRawPCM24k(request: req, provider: "openai-tts", startedAt: startedAt, firstAudioLogged: &firstAudioLogged)
    }

    private func streamRawPCM24k(request: URLRequest, provider: String, startedAt: Date, firstAudioLogged: inout Bool) async {
        do {
            let (bytes, response) = try await URLSession.shared.bytes(for: request)
            let code = (response as? HTTPURLResponse)?.statusCode ?? 0
            guard code == 200 else {
                AICallLog.write("\(tag) cascade TTS(\(provider)) HTTP \(code)")
                return
            }
            // Emit ~80 ms chunks (3840 bytes @ 24 kHz mono PCM16) as soon as they arrive
            var buf = Data()
            buf.reserveCapacity(4_096)
            for try await byte in bytes {
                if Task.isCancelled { return }
                buf.append(byte)
                if buf.count >= 3_840 {
                    if !firstAudioLogged {
                        firstAudioLogged = true
                        let ms = Int(Date().timeIntervalSince(startedAt) * 1000)
                        AICallLog.write("\(tag) cascade TTS(\(provider)) first audio +\(ms) ms")
                    }
                    onAudio?(buf)
                    buf.removeAll(keepingCapacity: true)
                }
            }
            if buf.count >= 2 {
                if buf.count % 2 == 1 { buf.removeLast() }
                onAudio?(buf)
            }
        } catch {
            AICallLog.write("\(tag) cascade TTS(\(provider)) error: \(error.localizedDescription)")
        }
    }
}
