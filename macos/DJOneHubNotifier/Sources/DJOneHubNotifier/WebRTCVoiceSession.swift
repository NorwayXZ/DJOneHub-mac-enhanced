import AudioToolbox
import Foundation

// MARK: - Native macOS AudioToolbox Opus Codec (48 kHz mono, 20 ms frames)

/// Encodes 24 kHz PCM16 into 48 kHz Opus 20 ms frames and decodes incoming
/// WebRTC Opus RTP payloads back into 24 kHz PCM16 using Apple's built-in
/// AudioToolbox — zero third-party C/C++ frameworks required.
final class NativeOpusCodec {
    private static let kNeedMoreData: OSStatus = 1001
    private static let frameSamples48k = 960 // 20 ms @ 48 kHz

    private var encoder: AudioConverterRef?
    private var decoder: AudioConverterRef?
    private var pending48k: [Int16] = []
    private var lastSample24k: Int16 = 0

    init?() {
        var pcmDesc = AudioStreamBasicDescription(
            mSampleRate: 48_000,
            mFormatID: kAudioFormatLinearPCM,
            mFormatFlags: kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked,
            mBytesPerPacket: 2,
            mFramesPerPacket: 1,
            mBytesPerFrame: 2,
            mChannelsPerFrame: 1,
            mBitsPerChannel: 16,
            mReserved: 0
        )
        var opusDesc = AudioStreamBasicDescription(
            mSampleRate: 48_000,
            mFormatID: kAudioFormatOpus,
            mFormatFlags: 0,
            mBytesPerPacket: 0,
            mFramesPerPacket: UInt32(Self.frameSamples48k),
            mBytesPerFrame: 0,
            mChannelsPerFrame: 1,
            mBitsPerChannel: 0,
            mReserved: 0
        )
        var enc: AudioConverterRef?
        var dec: AudioConverterRef?
        guard AudioConverterNew(&pcmDesc, &opusDesc, &enc) == noErr,
              AudioConverterNew(&opusDesc, &pcmDesc, &dec) == noErr,
              let enc, let dec else {
            if let enc { AudioConverterDispose(enc) }
            if let dec { AudioConverterDispose(dec) }
            return nil
        }
        var bitrate: UInt32 = 32_000
        _ = AudioConverterSetProperty(enc, kAudioConverterEncodeBitRate,
                                      UInt32(MemoryLayout<UInt32>.size), &bitrate)
        self.encoder = enc
        self.decoder = dec
    }

    deinit {
        if let encoder { AudioConverterDispose(encoder) }
        if let decoder { AudioConverterDispose(decoder) }
    }

    /// Feeds 24 kHz mono PCM16, returns zero or more 20 ms Opus packets.
    func encode24k(_ pcm24: Data) -> [Data] {
        let count = pcm24.count / 2
        guard count > 0 else { return [] }
        pending48k.reserveCapacity(pending48k.count + count * 2)
        pcm24.withUnsafeBytes { raw in
            let s = raw.bindMemory(to: Int16.self)
            var prev = Int(lastSample24k)
            for i in 0 ..< count {
                let cur = Int(Int16(littleEndian: s[i]))
                pending48k.append(Int16((prev + cur) / 2))
                pending48k.append(Int16(cur))
                prev = cur
            }
            lastSample24k = Int16(prev)
        }
        var packets: [Data] = []
        while pending48k.count >= Self.frameSamples48k {
            let slice = Array(pending48k.prefix(Self.frameSamples48k))
            pending48k.removeFirst(Self.frameSamples48k)
            if let pkt = encodeFrame48k(slice) {
                packets.append(pkt)
            }
        }
        return packets
    }

    /// Decodes one incoming WebRTC Opus RTP payload into 24 kHz mono PCM16.
    func decodeTo24k(_ opusPacket: Data) -> Data? {
        guard let decoder, !opusPacket.isEmpty else { return nil }
        let feed = Feed(opusPacket)
        feed.pktDesc = AudioStreamPacketDescription(
            mStartOffset: 0,
            mVariableFramesInPacket: 0,
            mDataByteSize: UInt32(opusPacket.count)
        )
        var out48k = [Int16](repeating: 0, count: 2_880) // up to 60 ms @ 48 kHz
        let outBytes = UInt32(out48k.count * 2)
        var outPackets: UInt32 = 2_880
        let st = out48k.withUnsafeMutableBytes { outPtr -> OSStatus in
            var abl = AudioBufferList(
                mNumberBuffers: 1,
                mBuffers: AudioBuffer(
                    mNumberChannels: 1,
                    mDataByteSize: outBytes,
                    mData: outPtr.baseAddress
                )
            )
            return AudioConverterFillComplexBuffer(
                decoder,
                { _, ioNumPackets, ioData, outPktDesc, userData in
                    let f = Unmanaged<Feed>.fromOpaque(userData!).takeUnretainedValue()
                    if f.used {
                        ioNumPackets.pointee = 0
                        return NativeOpusCodec.kNeedMoreData
                    }
                    f.used = true
                    ioNumPackets.pointee = 1
                    f.bytes.withUnsafeBytes { rawPtr in
                        ioData.pointee.mNumberBuffers = 1
                        ioData.pointee.mBuffers.mNumberChannels = 1
                        ioData.pointee.mBuffers.mDataByteSize = UInt32(f.bytes.count)
                        ioData.pointee.mBuffers.mData = UnsafeMutableRawPointer(mutating: rawPtr.baseAddress)
                    }
                    if let outPktDesc {
                        withUnsafeMutablePointer(to: &f.pktDesc) { outPktDesc.pointee = $0 }
                    }
                    return noErr
                },
                Unmanaged.passUnretained(feed).toOpaque(),
                &outPackets,
                &abl,
                nil
            )
        }
        guard (st == noErr || st == Self.kNeedMoreData), outPackets >= 2 else { return nil }
        let n24 = Int(outPackets) / 2
        var out24 = [Int16](repeating: 0, count: n24)
        for i in 0 ..< n24 {
            let a = Int(out48k[i * 2])
            let b = Int(out48k[i * 2 + 1])
            out24[i] = Int16((a + b) / 2).littleEndian
        }
        return out24.withUnsafeBytes { Data($0) }
    }

    private func encodeFrame48k(_ samples: [Int16]) -> Data? {
        guard let encoder else { return nil }
        let raw = samples.withUnsafeBytes { Data($0) }
        let feed = Feed(raw)
        var outBuf = [UInt8](repeating: 0, count: 1_500)
        let cap = UInt32(outBuf.count)
        var outPackets: UInt32 = 1
        var pktDesc = AudioStreamPacketDescription()
        let st = outBuf.withUnsafeMutableBytes { outPtr -> OSStatus in
            var abl = AudioBufferList(
                mNumberBuffers: 1,
                mBuffers: AudioBuffer(
                    mNumberChannels: 1,
                    mDataByteSize: cap,
                    mData: outPtr.baseAddress
                )
            )
            return AudioConverterFillComplexBuffer(
                encoder,
                { _, ioNumPackets, ioData, _, userData in
                    let f = Unmanaged<Feed>.fromOpaque(userData!).takeUnretainedValue()
                    if f.used {
                        ioNumPackets.pointee = 0
                        return NativeOpusCodec.kNeedMoreData
                    }
                    f.used = true
                    ioNumPackets.pointee = UInt32(f.bytes.count / 2)
                    f.bytes.withUnsafeBytes { rawPtr in
                        ioData.pointee.mNumberBuffers = 1
                        ioData.pointee.mBuffers.mNumberChannels = 1
                        ioData.pointee.mBuffers.mDataByteSize = UInt32(f.bytes.count)
                        ioData.pointee.mBuffers.mData = UnsafeMutableRawPointer(mutating: rawPtr.baseAddress)
                    }
                    return noErr
                },
                Unmanaged.passUnretained(feed).toOpaque(),
                &outPackets,
                &abl,
                &pktDesc
            )
        }
        guard (st == noErr || st == Self.kNeedMoreData), outPackets == 1, pktDesc.mDataByteSize > 0 else {
            return nil
        }
        return Data(outBuf.prefix(Int(pktDesc.mDataByteSize)))
    }

    private final class Feed {
        let bytes: [UInt8]
        var used = false
        var pktDesc = AudioStreamPacketDescription()
        init(_ data: Data) { self.bytes = [UInt8](data) }
    }
}

// MARK: - WebRTC Voice Session (via local Go pion/webrtc bridge)

/// Connects to OpenAI Realtime (`/v1/realtime/calls`) or Simultaneous Translation
/// (`/v1/realtime/translations/calls`) over real UDP WebRTC (Opus 48 kHz +
/// DataChannel `oai-events`) bridged by `djonehub-macos` at `127.0.0.1:7575`.
final class WebRTCVoiceSession: NSObject, VoiceAISession, @unchecked Sendable {
    enum BridgeMode: String {
        case realtime = "realtime"
        case translation = "translation"
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
    var onReady: ((Int) -> Void)?

    private let bridgeMode: BridgeMode
    private var task: URLSessionWebSocketTask?
    private let queue = DispatchQueue(label: "djonehub.ai.webrtc")
    private var codec: NativeOpusCodec?
    private var closed = false
    private var ready = false
    private var baseInstructions = ""
    private var queuedResponse: [String: Any]?
    private var answerAfterCurrent = false
    private var connectedAt = Date()
    private var firstAudioLogged = false
    private var firstSpeechAudioLogged = false
    private var speechHangoverChunks = 0
    private static let speechRMSThreshold = 0.004

    // Caption flush timers for continuous translation mode
    private var heardText = ""
    private var saidText = ""
    private var heardGen = 0
    private var saidGen = 0
    private static let captionPause: TimeInterval = 1.1

    private static let harmlessErrors: Set<String> = [
        "response_cancel_not_active",
        "input_audio_buffer_commit_empty",
        "conversation_already_has_active_response",
    ]

    init(mode: BridgeMode) {
        self.bridgeMode = mode
        super.init()
    }

    func connect(base: String, key: String, session: [String: Any]) {
        guard let opus = NativeOpusCodec() else {
            onError?("本机 Opus 编码器初始化失败")
            return
        }
        guard let url = URL(string: "ws://127.0.0.1:7575/api/ai/webrtc") else {
            onError?("本地 WebRTC 桥地址无效")
            return
        }
        queue.async { [weak self] in
            guard let self else { return }
            self.codec = opus
            self.connectedAt = Date()
            self.baseInstructions = session["instructions"] as? String ?? ""
            let task = URLSession.shared.webSocketTask(with: url)
            self.task = task
            task.resume()
            let initPayload: [String: Any] = [
                "mode": self.bridgeMode.rawValue,
                "base_url": base,
                "api_key": key,
                "session": session,
            ]
            AICallLog.write("\(self.tag) webrtc(\(self.bridgeMode.rawValue)): connecting via local pion bridge")
            self.sendText(initPayload)
            self.receive()
            self.queue.asyncAfter(deadline: .now() + 9.0) { [weak self] in
                guard let self, !self.closed, !self.ready else { return }
                self.closed = true
                self.task?.cancel(with: .normalClosure, reason: nil)
                self.onError?("WebRTC UDP 通道 9 秒未连通")
            }
        }
    }

    func appendAudio8k(_ pcm: Data) {
        appendAudio24k(PCM.up3(pcm))
    }

    func appendAudio24k(_ pcm: Data) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready, let codec = self.codec else { return }
            let packets = codec.encode24k(pcm)
            for pkt in packets {
                self.task?.send(.data(pkt)) { _ in }
            }
        }
    }

    func requestResponse(_ extra: String? = nil) {
        guard bridgeMode == .realtime else { return }
        var response: [String: Any] = [:]
        if let extra { response["instructions"] = baseInstructions + "\n\nRight now: " + extra }
        let event: [String: Any] = ["type": "response.create", "response": response]
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            if self.ready { self.sendText(event) } else { self.queuedResponse = event }
        }
    }

    func updateInstructions(_ text: String) {
        guard bridgeMode == .realtime else { return }
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            self.baseInstructions = text
            if self.ready {
                self.sendText([
                    "type": "session.update",
                    "session": ["type": "realtime", "instructions": text] as [String: Any],
                ])
            }
        }
    }

    func setTranslationLanguage(_ code: String) {
        guard bridgeMode == .translation else { return }
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            self.sendText([
                "type": "session.update",
                "session": ["audio": ["output": ["language": code]]] as [String: Any],
            ])
        }
    }

    func setNoiseReduction(_ type: String?) {
        queue.async { [weak self] in
            guard let self, !self.closed, self.ready else { return }
            let nrVal: Any = type.map { ["type": $0] as [String: Any] } ?? NSNull()
            if self.bridgeMode == .translation {
                self.sendText([
                    "type": "session.update",
                    "session": ["audio": ["input": ["noise_reduction": nrVal]]] as [String: Any],
                ])
            } else {
                self.sendText([
                    "type": "session.update",
                    "session": ["type": "realtime", "audio": ["input": ["noise_reduction": nrVal]]] as [String: Any],
                ])
            }
        }
    }

    func close() {
        queue.async { [weak self] in
            guard let self, !self.closed else { return }
            self.closed = true
            self.flushTranslationDrafts()
            if self.bridgeMode == .translation && self.ready {
                self.sendText(["type": "session.close"])
            }
            let t = self.task
            self.task = nil
            self.codec = nil
            DispatchQueue.global().asyncAfter(deadline: .now() + 0.2) {
                t?.cancel(with: .normalClosure, reason: nil)
            }
        }
    }

    /// Live WebRTC UDP handshake check used by Settings -> 检测连接.
    static func testConnection(base: String, key: String, model: String, voice: String) async -> TranslateStreamSession.TestOutcome {
        let session = WebRTCVoiceSession(mode: .realtime)
        session.tag = "test-rtc"
        let cfg: [String: Any] = [
            "type": "realtime",
            "model": model,
            "instructions": "Reply with one word.",
            "audio": ["output": ["voice": voice]],
        ]
        return await withCheckedContinuation { cont in
            let lock = NSLock()
            var done = false
            func finish(_ r: TranslateStreamSession.TestOutcome) {
                lock.lock(); defer { lock.unlock() }
                guard !done else { return }
                done = true
                session.close()
                cont.resume(returning: r)
            }
            session.onReady = { ms in finish(.ok(ms)) }
            session.onError = { msg in finish(.fail(msg)) }
            session.connect(base: base, key: key, session: cfg)
            DispatchQueue.global().asyncAfter(deadline: .now() + 10) {
                finish(.fail("WebRTC 10 秒超时"))
            }
        }
    }

    // MARK: - Private

    private func sendText(_ event: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: event),
              let text = String(data: data, encoding: .utf8) else { return }
        task?.send(.string(text)) { [weak self] error in
            if let error, let self, !self.closed {
                self.onError?("WebRTC 桥断开：\(error.localizedDescription)")
            }
        }
    }

    private func receive() {
        task?.receive { [weak self] result in
            guard let self, !self.closed else { return }
            switch result {
            case .failure(let err):
                self.closed = true
                self.onError?("WebRTC 本地桥失败：\(err.localizedDescription)")
            case .success(let message):
                switch message {
                case .data(let opus):
                    self.queue.async { self.handleOpus(opus) }
                case .string(let text):
                    self.queue.async { self.handleText(text) }
                @unknown default:
                    break
                }
                self.receive()
            }
        }
    }

    private func handleOpus(_ opus: Data) {
        guard !closed, let pcm = codec?.decodeTo24k(opus), !pcm.isEmpty else { return }
        if !firstAudioLogged {
            firstAudioLogged = true
            let ms = Int(Date().timeIntervalSince(connectedAt) * 1000)
            AICallLog.write("\(tag) webrtc: first RTP Opus audio at +\(ms) ms")
        }
        // Filter idle silence frames (both realtime comfort noise and translation idle packets).
        let rms = PCM.rms16(pcm)
        if rms >= Self.speechRMSThreshold {
            speechHangoverChunks = 8 // 8 * 20 ms = 160 ms smooth natural tail
            if !firstSpeechAudioLogged {
                firstSpeechAudioLogged = true
                let ms = Int(Date().timeIntervalSince(connectedAt) * 1000)
                AICallLog.write(String(format: "%@ webrtc: first SPEECH RTP at +%d ms rms=%.4f", tag, ms, rms))
            }
            onAudio?(pcm)
        } else if speechHangoverChunks > 0 {
            speechHangoverChunks -= 1
            onAudio?(pcm)
        }
    }

    private func handleText(_ text: String) {
        guard let data = text.data(using: .utf8),
              let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }

        // Local bridge control messages
        if let bridge = event["_bridge"] as? String {
            switch bridge {
            case "ready":
                let ms = (event["ice_ms"] as? Int) ?? Int(Date().timeIntervalSince(connectedAt) * 1000)
                AICallLog.write("\(tag) webrtc(\(bridgeMode.rawValue)): ICE+DC ready in \(ms) ms")
                ready = true
                if let r = queuedResponse { queuedResponse = nil; sendText(r) }
                onReady?(ms)
            case "error":
                let msg = (event["message"] as? String) ?? "WebRTC 错误"
                AICallLog.write("\(tag) webrtc bridge error: \(msg)")
                closed = true
                onError?(msg)
            default:
                break
            }
            return
        }

        guard let type = event["type"] as? String else { return }
        if !type.hasSuffix(".delta") {
            AICallLog.write("\(tag) rtc " + (type == "error" ? "error \(text.prefix(300))" : type))
        }

        if bridgeMode == .translation {
            handleTranslationEvent(type, event: event)
        } else {
            handleRealtimeEvent(type, event: event)
        }
    }

    private func handleTranslationEvent(_ type: String, event: [String: Any]) {
        switch type {
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
            if !ready { onError?("WebRTC 同传出错：\(msg)") }
        default:
            break
        }
    }

    private func handleRealtimeEvent(_ type: String, event: [String: Any]) {
        switch type {
        case "response.output_audio_transcript.delta", "response.audio_transcript.delta":
            if let d = event["delta"] as? String, !d.isEmpty { onSaidDelta?(d) }
        case "conversation.item.input_audio_transcription.delta":
            if let d = event["delta"] as? String, !d.isEmpty { onHeardDelta?(d) }
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
            onSpeechStopped?()
        case "response.done":
            if answerAfterCurrent, !closed {
                answerAfterCurrent = false
                sendText(["type": "response.create", "response": [:] as [String: Any]])
            }
        case "error":
            let err = event["error"] as? [String: Any]
            let msg = (err?["message"] as? String) ?? "未知错误"
            let code = (err?["code"] as? String) ?? ""
            if code == "conversation_already_has_active_response" || msg.lowercased().contains("active response in progress") {
                answerAfterCurrent = true
            } else if Self.harmlessErrors.contains(code) {
                break
            } else if ready {
                AICallLog.write("\(tag) rtc non-fatal error \(code): \(msg)")
            } else {
                onError?("WebRTC AI 出错：\(msg)")
            }
        default:
            break
        }
    }

    private func flushTranslationDrafts() {
        heardGen += 1
        saidGen += 1
        let h = heardText.trimmingCharacters(in: .whitespacesAndNewlines)
        heardText = ""
        if !h.isEmpty { onHeard?(h) }
        let s = saidText.trimmingCharacters(in: .whitespacesAndNewlines)
        saidText = ""
        if !s.isEmpty { onSaid?(s) }
    }
}
