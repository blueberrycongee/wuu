import AVFoundation
import Foundation
import Speech

private struct Event: Encodable {
    let type: String
    let state: String?
    let text: String?
    let isFinal: Bool?
    let level: Double?
    let code: String?
    let message: String?
    let status: String?

    enum CodingKeys: String, CodingKey {
        case type, state, text, level, code, message, status
        case isFinal = "is_final"
    }

    init(
        type: String,
        state: String? = nil,
        text: String? = nil,
        isFinal: Bool? = nil,
        level: Double? = nil,
        code: String? = nil,
        message: String? = nil,
        status: String? = nil
    ) {
        self.type = type
        self.state = state
        self.text = text
        self.isFinal = isFinal
        self.level = level
        self.code = code
        self.message = message
        self.status = status
    }

    static func state(_ value: String) -> Event {
        Event(type: "state", state: value)
    }

    static func level(_ value: Double) -> Event {
        Event(type: "level", level: value)
    }

    static func result(_ text: String, final: Bool) -> Event {
        Event(type: "result", text: text, isFinal: final)
    }

    static func error(_ code: String, _ message: String) -> Event {
        Event(type: "error", code: code, message: message)
    }

    static func authorizationStatus(_ status: String) -> Event {
        Event(type: "authorization_status", status: status)
    }
}

private let eventOutputQueue = DispatchQueue(label: "com.blueberrycongee.wuu.speech-events")

private func emit(_ event: Event) {
    guard let data = try? JSONEncoder().encode(event),
          let line = String(data: data, encoding: .utf8) else {
        return
    }
    eventOutputQueue.sync {
        FileHandle.standardOutput.write(Data((line + "\n").utf8))
    }
}

private final class SpeechSession {
    private let localeIdentifier: String
    private let audioEngine = AVAudioEngine()
    private var recognitionTask: SFSpeechRecognitionTask?
    private var recognitionRequest: SFSpeechAudioBufferRecognitionRequest?
    private var stopped = false
    private var finishing = false
    private var tapInstalled = false
    private var lastLevelEmissionTime = 0.0

    init(localeIdentifier: String) {
        self.localeIdentifier = localeIdentifier
    }

    func start() {
        guard SFSpeechRecognizer.supportedLocales().contains(where: {
            $0.identifier.replacingOccurrences(of: "_", with: "-").caseInsensitiveCompare(localeIdentifier.replacingOccurrences(of: "_", with: "-")) == .orderedSame
        }) else {
            emit(.error("locale_unavailable", "The selected language is not supported by macOS Speech Recognition."))
            exit(2)
        }

        guard let recognizer = SFSpeechRecognizer(locale: Locale(identifier: localeIdentifier)) else {
            emit(.error("locale_unavailable", "The selected language is not supported by macOS Speech Recognition."))
            exit(2)
        }
        guard recognizer.isAvailable else {
            emit(.error("recognizer_unavailable", "macOS Speech Recognition is currently unavailable."))
            exit(2)
        }
        guard recognizer.supportsOnDeviceRecognition else {
            emit(.error("on_device_unavailable", "On-device speech recognition is unavailable for the selected language."))
            exit(2)
        }

        emit(.state("requesting_speech_permission"))
        SFSpeechRecognizer.requestAuthorization { [weak self] status in
            DispatchQueue.main.async {
                guard let self, !self.finishing, !self.stopped else { return }
                switch status {
                case .authorized:
                    self.beginRecognition(with: recognizer)
                case .denied:
                    emit(.error("speech_permission_denied", "Speech Recognition permission was denied."))
                    exit(3)
                case .restricted:
                    emit(.error("speech_permission_restricted", "Speech Recognition is restricted on this Mac."))
                    exit(3)
                case .notDetermined:
                    emit(.error("speech_permission_unavailable", "Speech Recognition permission could not be determined."))
                    exit(3)
                @unknown default:
                    emit(.error("speech_permission_unavailable", "Speech Recognition permission is unavailable."))
                    exit(3)
                }
            }
        }
    }

    func stop() {
        guard !stopped, !finishing else { return }
        finishing = true
        stopAudio()
        guard let recognitionRequest else { complete(); return }
        emit(.state("finalizing"))
        recognitionRequest.endAudio()
        // endAudio drains the recognizer; cancellation would lose the final words.
        DispatchQueue.main.asyncAfter(deadline: .now() + 12) { [weak self] in
            guard let self, !self.stopped else { return }
            emit(.error("recognition_timeout", "Speech recognition did not finish."))
            self.complete()
        }
    }

    private func stopAudio() {
        audioEngine.stop()
        if tapInstalled {
            audioEngine.inputNode.removeTap(onBus: 0)
            tapInstalled = false
        }
    }

    private func complete() {
        stopped = true
        stopAudio()
        recognitionTask?.cancel()
        exit(0)
    }

    private func beginRecognition(with recognizer: SFSpeechRecognizer) {
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        request.requiresOnDeviceRecognition = true
        recognitionRequest = request

        let inputNode = audioEngine.inputNode
        let format = inputNode.outputFormat(forBus: 0)
        guard format.sampleRate > 0, format.channelCount > 0 else {
            emit(.error("microphone_unavailable", "No usable microphone input is available."))
            exit(4)
        }
        inputNode.installTap(onBus: 0, bufferSize: 1024, format: format) { [weak self] buffer, _ in
            request.append(buffer)
            self?.emitAudioLevel(from: buffer)
        }

        tapInstalled = true
        recognitionTask = recognizer.recognitionTask(with: request) { [weak self] result, error in
            guard let self, !self.stopped else { return }
            if let result {
                emit(.result(result.bestTranscription.formattedString, final: result.isFinal))
                if result.isFinal {
                    self.complete()
                    return
                }
            }
            if let error {
                emit(.error("recognition_failed", error.localizedDescription))
                exit(5)
            }
        }

        do {
            audioEngine.prepare()
            try audioEngine.start()
            emit(.state("listening"))
        } catch {
            emit(.error("audio_engine_failed", error.localizedDescription))
            exit(4)
        }
    }

    private func emitAudioLevel(from buffer: AVAudioPCMBuffer) {
        let now = ProcessInfo.processInfo.systemUptime
        guard now - lastLevelEmissionTime >= 0.05,
              let channels = buffer.floatChannelData else {
            return
        }
        lastLevelEmissionTime = now

        let frameCount = Int(buffer.frameLength)
        let channelCount = Int(buffer.format.channelCount)
        guard frameCount > 0, channelCount > 0 else { return }

        var sumOfSquares: Float = 0
        for channelIndex in 0..<channelCount {
            let samples = channels[channelIndex]
            for frameIndex in 0..<frameCount {
                let sample = samples[frameIndex]
                sumOfSquares += sample * sample
            }
        }
        let sampleCount = Float(frameCount * channelCount)
        let rootMeanSquare = sqrt(sumOfSquares / sampleCount)
        let decibels = 20 * log10(max(rootMeanSquare, 0.000_01))
        let normalized = min(max((Double(decibels) + 60) / 54, 0), 1)
        emit(.level(normalized))
    }
}

if CommandLine.arguments.contains("--authorization-status") {
    let status: String
    switch SFSpeechRecognizer.authorizationStatus() {
    case .authorized:
        status = "granted"
    case .denied:
        status = "denied"
    case .restricted:
        status = "restricted"
    case .notDetermined:
        status = "not_determined"
    @unknown default:
        status = "unknown"
    }
    emit(.authorizationStatus(status))
    exit(0)
}

let localeFlagIndex = CommandLine.arguments.firstIndex(of: "--locale")
let localeIdentifier = localeFlagIndex.flatMap { index -> String? in
    let valueIndex = CommandLine.arguments.index(after: index)
    return valueIndex < CommandLine.arguments.endIndex ? CommandLine.arguments[valueIndex] : nil
} ?? Locale.current.identifier

private let session = SpeechSession(localeIdentifier: localeIdentifier)
FileHandle.standardInput.readabilityHandler = { handle in
    let data = handle.availableData
    DispatchQueue.main.async {
        if data.isEmpty { exit(0) }
        session.stop()
    }
}
session.start()
RunLoop.main.run()
