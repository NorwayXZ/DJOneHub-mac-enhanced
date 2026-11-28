import Foundation
import Combine

/// 原生主窗口的数据中心：轮询后台 /api/calls/status，驱动拨号、通话与最近通话界面。
@MainActor
final class CallCenter: ObservableObject {
    @Published var activeCall: CallRecord?
    @Published var history: [CallRecord] = []
    @Published var isOnline = false
    @Published var lastError: String?
    @Published var lastActionError: String?
    @Published var isMuted = false
    @Published var isRecording = false
    @Published var numberInput = ""
    @Published var isDialing = false

    private let api: DJOneHubAPI
    var apiClient: DJOneHubAPI { api }
    private var pollTask: Task<Void, Never>?
    private var lastActiveID: String?
    private var pollInFlight = false
    private let maVoAudio = VoiceAudioService()
    private var maVoAudioStarting = false
    private var maVoAudioCallID: String?
    private var maVoHostRegistered = false
    /// The call AI (interpreter / talks for you).
    let ai = AICallAssistant()
    private var aiScreenPending = false
    private var autoScreenTask: Task<Void, Never>?
    private var activeSeenAt: Date?

    /// 新来电（呼入且响铃/等待）时回调，用于弹窗/聚焦主窗口。
    var onIncoming: ((CallRecord) -> Void)?

    init(api: DJOneHubAPI) {
        self.api = api
    }

    func start() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.pollOnce()
                // While a call is ringing, look more often so the pickup (and
                // the AI's first words) aren't held back by the poll interval.
                let ringing = await MainActor.run { () -> Bool in
                    guard let state = self?.activeCall?.state else { return false }
                    return ["dialing", "alerting", "incoming", "waiting"].contains(state)
                }
                try? await Task.sleep(nanoseconds: ringing ? 300_000_000 : 1_000_000_000)
            }
        }
    }

    func stop() {
        pollTask?.cancel()
        pollTask = nil
        maVoAudio.stop()
        maVoHostRegistered = false
        Task { _ = try? await api.setMaVoAudioHostEnabled(false) }
    }

    private func pollOnce() async {
        guard !pollInFlight else { return }
        pollInFlight = true
        defer { pollInFlight = false }
        do {
            let status = try await api.callStatus()
            isOnline = true
            if !maVoHostRegistered {
                do {
                    try await api.setMaVoAudioHostEnabled(true)
                    maVoHostRegistered = true
                } catch {
                    // The backend can take tens of seconds to enumerate USB
                    // after a restart. Keep the old route until registration
                    // succeeds, then retry on the next poll.
                    lastError = error.localizedDescription
                }
            }
            lastError = status.lastPollError
            let previousID = activeCall?.id
            activeCall = status.active
            history = status.history ?? []

            if status.active == nil, previousID != nil {
                ai.callEnded()
                aiScreenPending = false
                autoScreenTask?.cancel()
                if isRecording { maVoAudio.stopRecording { _ in } }
                isRecording = false
                maVoAudio.stop()
                maVoAudioCallID = nil
                maVoAudioStarting = false
            }
            if status.active == nil, previousID != nil, isRecording {
                _ = try? await api.setCallRecording(false)
                isRecording = false
            }

            if let active = status.active,
               active.direction == "incoming",
               active.state == "incoming" || active.state == "waiting",
               active.id != lastActiveID {
                lastActiveID = active.id
                onIncoming?(active)
                scheduleAutoScreen(for: active)
            } else if status.active == nil {
                lastActiveID = nil
            }
            if status.active?.id != previousID, status.active == nil || previousID == nil {
                isMuted = false
            }
            if let active = status.active, active.state == "active" {
                if activeSeenAt == nil { activeSeenAt = Date() }
                startMaVoAudioIfNeeded(for: active)
            } else {
                activeSeenAt = nil
            }
            // Prewarm interpreter ONLY when dialing with AI translate requested,
            // so normal personal calls never hold idle WebSocket sessions.
            if let active = status.active, ai.pendingTranslate, ai.pendingGoal == nil, !aiScreenPending,
               ["dialing", "alerting", "active"].contains(active.state) {
                ai.prewarmInterpreter(
                    callID: active.id,
                    number: active.number,
                    preferredLanguage: ai.pendingLanguage ?? CallLanguage.preferred(for: active.number)
                )
            }
            // Load the customer's previous call brief as soon as a call appears.
            if let active = status.active, active.id != previousID {
                ai.loadPreviousCall(for: active.number, excludingID: active.id)
                if ai.pendingLanguage == nil {
                    ai.language = CallLanguage.preferred(for: active.number)
                }
            }
            // Outgoing AI call still ringing: get the AI connected now.
            if let active = status.active, active.direction != "incoming",
               active.state == "dialing" || active.state == "alerting",
               let goal = ai.pendingGoal, !ai.hasPrewarmed {
                ai.prewarmAgent(goal: goal,
                                language: ai.pendingLanguage ?? CallLanguage.preferred(for: active.number),
                                audio: maVoAudio)
            }
        } catch {
            isOnline = false
            lastError = error.localizedDescription
        }
    }

    func dial(_ override: String? = nil) {
        let number = (override ?? numberInput).trimmingCharacters(in: .whitespaces)
        guard !number.isEmpty else { return }
        isDialing = true
        Task {
            do {
                try await api.dial(number: number)
                await MainActor.run {
                    self.isDialing = false
                    self.lastActionError = nil
                }
            } catch {
                await MainActor.run {
                    self.isDialing = false
                    self.lastActionError = "拨号失败：\(error.localizedDescription)"
                }
            }
        }
    }

    func answer() {
        Task {
            do {
                try await api.answerCall()
                lastActionError = nil
            } catch {
                lastActionError = "接听失败：\(error.localizedDescription)"
            }
        }
    }

    func reject() {
        Task {
            do {
                _ = try await api.rejectCall()
                lastActionError = nil
            } catch {
                lastActionError = "拒接失败：\(error.localizedDescription)"
            }
        }
    }

    func hangup() {
        ai.callEnded()
        isMuted = false
        if isRecording { maVoAudio.stopRecording { _ in } }
        isRecording = false
        maVoAudio.stop()
        Task {
            do {
                try await api.hangupCall()
                lastActionError = nil
            } catch {
                lastActionError = "挂断失败：\(error.localizedDescription)"
            }
        }
    }

    func sendDTMF(_ digit: String) {
        Task { _ = try? await api.sendDTMF(digit: digit) }
    }

    func toggleMute() {
        let muted = !isMuted
        isMuted = muted
        maVoAudio.setMuted(muted)
    }

    func toggleRecording() {
        guard maVoAudio.isRunning else {
            lastActionError = "通话音频尚未连接，无法开始录音。"
            return
        }
        if isRecording {
            maVoAudio.stopRecording { [weak self] _ in self?.isRecording = false }
            return
        }
        do {
            _ = try maVoAudio.startRecording()
            isRecording = true
            lastActionError = nil
        } catch {
            lastActionError = "无法创建录音：\(error.localizedDescription)"
        }
    }

    private func startMaVoAudioIfNeeded(for call: CallRecord) {
        guard !maVoAudio.isRunning, !maVoAudioStarting, maVoAudioCallID != call.id else { return }
        AICallLog.write("timing: call active seen, starting audio")
        maVoAudioStarting = true
        maVoAudioCallID = call.id
        // Keep MaVo's ordering: acquire microphone permission before creating
        // the engine or opening the UAC media bridge. AVAudioEngine startup is
        // intentionally all-or-nothing in the upstream service.
        maVoAudio.requestMicrophoneAccess { [weak self] granted in
            guard let self else { return }
            guard granted else {
                self.maVoAudioStarting = false
                self.maVoAudioCallID = nil
                self.lastActionError = "需要麦克风权限才能进行双向通话。"
                return
            }
            Task {
                do {
                    AICallLog.write("timing: mic ok, waiting for route")
                    let config = try await self.waitForMaVoRoute()
                    AICallLog.write("timing: route ready, opening UAC")
                    self.maVoAudio.startUAC(
                        vendorID: config.vendorID,
                        productID: config.productID,
                        matchingLocationID: config.locationID
                    ) { [weak self] result in
                        guard let self else { return }
                        self.maVoAudioStarting = false
                        AICallLog.write("timing: UAC result \(result)")
                        switch result {
                        case .success:
                            self.maVoAudio.setMediaEnabled(true)
                            self.lastActionError = nil
                            self.startPendingAI(for: call)
                        case .failure(let message):
                            self.lastActionError = "MaVo 音频启动失败：\(message)"
                            self.maVoAudioCallID = nil
                        }
                    }
                } catch {
                    self.maVoAudioStarting = false
                    self.maVoAudioCallID = nil
                    self.lastActionError = "MaVo 音频准备失败：\(error.localizedDescription)"
                }
            }
        }
    }

    private func waitForMaVoRoute() async throws -> MaVoAudioHostConfig {
        let deadline = Date().addingTimeInterval(40)
        while Date() < deadline {
            let config = try await api.maVoAudioHostConfig()
            if config.routeReady {
                return config
            }
            if let routeError = config.routeError, !routeError.isEmpty {
                throw APIError.http(409, "模块语音路由准备失败：\(routeError)")
            }
            try await Task.sleep(for: .milliseconds(500))
        }
        throw APIError.http(409, "等待模块 UAC 语音路由超过 40 秒。请在设置中查看初始化诊断。")
    }

    func callDuration(now: Date = Date()) -> TimeInterval {
        guard let call = activeCall else { return 0 }
        let end = call.endedAt ?? now
        return max(0, end.timeIntervalSince(call.startedAt))
    }

    // MARK: - Call AI

    /// Dial and automatically launch simultaneous translation as soon as they pick up.
    func dialWithTranslate(_ number: String, language: CallLanguage) {
        ai.pendingGoal = nil
        ai.pendingTranslate = true
        ai.pendingLanguage = language
        ai.language = language
        CallLanguageMemory.remember(language, for: number)
        dial(number)
    }

    /// Dial and let the AI talk once the other side picks up.
    func dialWithAI(_ number: String, goal: String, language: CallLanguage) {
        ai.pendingTranslate = false
        ai.pendingGoal = goal
        ai.pendingLanguage = language
        dial(number)
    }

    /// Answer an incoming call and let the AI ask who it is.
    func answerWithAI() {
        aiScreenPending = true
        answer()
    }

    func startAITranslate() {
        guard let call = activeCall else { return }
        ai.startTranslate(callID: call.id, number: call.number, audio: maVoAudio)
    }

    func startAIAgent(goal: String) {
        guard let call = activeCall else { return }
        ai.startAgent(callID: call.id, number: call.number, goal: goal, screening: false,
                      audio: maVoAudio, speakFirst: true)
    }

    func aiTakeOver() {
        ai.takeOver()
        isMuted = false
        maVoAudio.setMuted(false)
    }

    private func startPendingAI(for call: CallRecord) {
        let late = activeSeenAt.map { Date().timeIntervalSince($0) > 2 } ?? false
        if aiScreenPending {
            aiScreenPending = false
            ai.language = CallLanguage.preferred(for: call.number)
            ai.startAgent(callID: call.id, number: call.number, goal: "", screening: true,
                          audio: maVoAudio, speakFirst: true)
        } else if let goal = ai.pendingGoal, call.direction != "incoming" {
            ai.language = ai.pendingLanguage ?? CallLanguage.preferred(for: call.number)
            ai.pendingGoal = nil
            ai.startAgent(callID: call.id, number: call.number, goal: goal, screening: false,
                          audio: maVoAudio, speakFirst: true, lateStart: late)
        } else if ai.pendingTranslate, call.direction != "incoming" {
            ai.language = ai.pendingLanguage ?? CallLanguage.preferred(for: call.number)
            ai.pendingTranslate = false
            ai.pendingLanguage = nil
            ai.startTranslate(callID: call.id, number: call.number, audio: maVoAudio)
        } else {
            ai.language = CallLanguage.preferred(for: call.number)
        }
    }

    /// With "AI 自动接听" on, the AI picks up after a few rings.
    private func scheduleAutoScreen(for call: CallRecord) {
        autoScreenTask?.cancel()
        guard AICallSettings.shared.autoScreenIncoming, AICallSettings.shared.isConfigured,
              call.audioWarning == nil else { return }
        autoScreenTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(6))
            guard let self, !Task.isCancelled,
                  let now = self.activeCall, now.id == call.id,
                  now.state == "incoming" || now.state == "waiting" else { return }
            self.answerWithAI()
        }
    }

    func dialNumber(_ number: String) {
        numberInput = number
        dial()
    }
}
