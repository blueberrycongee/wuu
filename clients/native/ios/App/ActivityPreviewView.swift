import SwiftUI
import WuuCore
import AVFoundation
import VideoToolbox
import ImageIO

/// A visible viewer owns its lease. Closing it never interrupts the agent.
@MainActor struct ActivityPreviewView: View {
    let activity: PreviewActivity
    let connection: RemoteConnection?
    var fixtureImage: Data? = nil
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var phase
    @State private var surface = PreviewSurface()
    @State private var status = "loading"
    @State private var message = ""
    @State private var retry = 0
    var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()
                PreviewSurfaceView(surface: surface)
                    .accessibilityLabel(activity.kind == "browser" ? "浏览器实时画面" : "电脑实时画面")
                if status == "loading" { ProgressView().tint(.white).accessibilityLabel("正在连接预览") }
                if status == "error" || status == "ended" || status == "paused" {
                    VStack(spacing: 12) {
                        Text(message.isEmpty ? fallbackStatus : message).foregroundStyle(.white).multilineTextAlignment(.center)
                        if status == "error" {
                            Button { retry += 1 } label: {
                                Image(systemName: "arrow.clockwise").font(.system(size: 19)).frame(width: 44, height: 44)
                            }.tint(.white).accessibilityLabel("重新连接预览")
                        }
                    }.padding(20).background(.black.opacity(0.65), in: RoundedRectangle(cornerRadius: 12)).padding(20)
                }
            }
            .navigationTitle(activity.kind == "browser" ? "浏览器" : "电脑")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button { dismiss() } label: { Image(systemName: "xmark").frame(width: 44, height: 44) }
                        .accessibilityLabel("关闭预览")
                }
            }
        }
        .task(id: retry) { await observe() }
        .onChange(of: phase) { _, value in if value != .active { dismiss() } }
        .background {
            #if DEBUG
            if fixtureImage != nil { NativePreviewFixtureReadyMarker(name: "viewer", ready: status == "live") }
            #endif
        }
    }
    private var fallbackStatus: String {
        switch status { case "paused": "预览已暂停"; case "ended": "预览已结束"; default: "无法连接预览" }
    }
    private func observe() async {
        #if DEBUG
        if let fixtureImage {
            var decodedCount = 0
            let video = ProcessInfo.processInfo.arguments.contains("--native-preview-h264-fixture")
            do {
                let frames: [PreviewFrame]
                if video { frames = try NativeUIFixture.previewVideoFrames() }
                else {
                    frames = [try PreviewFrame(["stream_id": "fixture", "sequence": 1, "generation": 1, "configuration": 1,
                        "codec": "jpeg", "keyframe": true, "timestamp": 0, "width": 960, "height": 600,
                        "data": .string(fixtureImage.base64EncodedString())])]
                }
                let decoder = PreviewDecoder()
                for frame in frames {
                    let decoded = try await decoder.decode(frame)
                    try Task.checkCancellation()
                    try surface.present(decoded, size: CGSize(width: frame.width, height: frame.height))
                    decodedCount += 1
                }
                status = "live"
            } catch { status = "error"; message = error.localizedDescription }
            if video {
                let evidence: JSONValue = ["codec": "h264", "decoded_frames": .number(Double(decodedCount)),
                    "submitted_frames": .number(Double(decodedCount)), "error": .string(message)]
                let url = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
                    .appendingPathComponent("preview-fixture-result.json")
                try? JSONEncoder().encode(evidence).write(to: url, options: .atomic)
            }
            return
        }
        #endif
        guard let connection else { status = "error"; return }
        // A corrupt frame requires a new stream/IDR, never skipping compressed deltas in place.
        for attempt in 0..<2 {
            guard !Task.isCancelled else { return }
            status = "loading"; message = ""; surface.clear()
            let decoder = PreviewDecoder()
            var subscription: PreviewSubscription?
            var renewal: Task<Void, Never>?
            do {
                let opened = try await connection.openPreview(activity)
                subscription = opened
                try Task.checkCancellation()
                try await connection.renewPreview(opened.id)
                renewal = Task {
                    do {
                        while !Task.isCancelled {
                            try await Task.sleep(for: .seconds(2))
                            try await connection.renewPreview(opened.id)
                        }
                    } catch {
                        if !Task.isCancelled { await connection.closePreview(opened.id) }
                    }
                }
                for try await event in opened.events {
                    try Task.checkCancellation()
                    switch event {
                    case .frame(let frame):
                        let image = try await decoder.decode(frame)
                        try Task.checkCancellation()
                        try surface.present(image, size: CGSize(width: frame.width, height: frame.height))
                        if status != "live" { status = "live" }; if !message.isEmpty { message = "" }
                        // Decode every compressed frame, then release credit after display submission.
                        try await connection.acknowledgePreview(opened.id, sequence: frame.sequence)
                    case .state(let state, let detail):
                        status = state; message = detail
                        if state == "error" { throw NativeError.invalid(detail.isEmpty ? "预览不可用" : detail) }
                        if state == "ended" { renewal?.cancel(); await connection.closePreview(opened.id); return }
                    }
                }
                renewal?.cancel()
                await connection.closePreview(opened.id)
                if !Task.isCancelled { status = "ended" }
                return
            } catch {
                renewal?.cancel()
                if let subscription { await connection.closePreview(subscription.id) }
                guard !Task.isCancelled else { return }
                if attempt == 1 { status = "error"; message = error.localizedDescription }
            }
        }
    }
}

private enum DecodedPreview: @unchecked Sendable {
    case image(CGImage)
    case video(CVPixelBuffer)
}

/// Serial decoding is off the main actor; at most the host's three credits can wait.
private actor PreviewDecoder {
    private var session: VTDecompressionSession?
    private var format: CMFormatDescription?
    private var generation = 0
    private var configuration = 0
    deinit { if let session { VTDecompressionSessionInvalidate(session) } }
    func decode(_ frame: PreviewFrame) throws -> DecodedPreview {
        if frame.codec == "jpeg" {
            guard let source = CGImageSourceCreateWithData(frame.data as CFData, nil),
                  let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
                  let width = properties[kCGImagePropertyPixelWidth] as? Int,
                  let height = properties[kCGImagePropertyPixelHeight] as? Int,
                  width == frame.width, height == frame.height,
                  let image = CGImageSourceCreateThumbnailAtIndex(source, 0,
                    [kCGImageSourceCreateThumbnailFromImageAlways: true, kCGImageSourceShouldCacheImmediately: true,
                     kCGImageSourceThumbnailMaxPixelSize: 1280] as CFDictionary) else {
                throw NativeError.invalid("无法解码预览图片")
            }
            return .image(image)
        }
        if session == nil || generation != frame.generation || configuration != frame.configuration {
            if let session { VTDecompressionSessionInvalidate(session) }
            session = nil; format = nil
            guard frame.keyframe, let description = frame.description else { throw NativeError.invalid("预览需要新的关键帧") }
            let next = try videoFormat(description)
            let dimensions = CMVideoFormatDescriptionGetDimensions(next)
            guard Int(dimensions.width) == frame.width, Int(dimensions.height) == frame.height,
                  dimensions.width > 0, dimensions.width <= 1280, dimensions.height > 0, dimensions.height <= 1280 else {
                throw NativeError.invalid("预览视频尺寸超出限制")
            }
            var decoder: VTDecompressionSession?
            try check(VTDecompressionSessionCreate(allocator: kCFAllocatorDefault, formatDescription: next,
                decoderSpecification: nil, imageBufferAttributes: nil, outputCallback: nil, decompressionSessionOut: &decoder))
            format = next; session = decoder; generation = frame.generation; configuration = frame.configuration
        }
        guard let session, let format else { throw NativeError.invalid("无法启动预览解码器") }
        var block: CMBlockBuffer?
        try check(CMBlockBufferCreateWithMemoryBlock(allocator: kCFAllocatorDefault, memoryBlock: nil,
            blockLength: frame.data.count, blockAllocator: kCFAllocatorDefault, customBlockSource: nil,
            offsetToData: 0, dataLength: frame.data.count, flags: 0, blockBufferOut: &block))
        guard let block else { throw NativeError.invalid("无法读取预览帧") }
        try frame.data.withUnsafeBytes { bytes in
            try check(CMBlockBufferReplaceDataBytes(with: bytes.baseAddress!, blockBuffer: block,
                offsetIntoDestination: 0, dataLength: frame.data.count))
        }
        var sample: CMSampleBuffer?
        var timing = CMSampleTimingInfo(duration: .invalid, presentationTimeStamp: CMTime(value: frame.timestamp, timescale: 1_000_000), decodeTimeStamp: .invalid)
        var count = frame.data.count
        try check(CMSampleBufferCreateReady(allocator: kCFAllocatorDefault, dataBuffer: block, formatDescription: format,
            sampleCount: 1, sampleTimingEntryCount: 1, sampleTimingArray: &timing,
            sampleSizeEntryCount: 1, sampleSizeArray: &count, sampleBufferOut: &sample))
        guard let sample else { throw NativeError.invalid("无法读取预览帧") }
        var output: DecodedPreview?
        var failure: OSStatus = noErr
        // With both async flags clear, VideoToolbox calls this handler before returning.
        let result = VTDecompressionSessionDecodeFrame(session, sampleBuffer: sample, flags: [], infoFlagsOut: nil) { code, _, image, _, _ in
            failure = code; output = image.map(DecodedPreview.video)
        }
        try check(result); try check(failure)
        guard case .video(let decoded)? = output,
              CVPixelBufferGetWidth(decoded) == frame.width, CVPixelBufferGetHeight(decoded) == frame.height else {
            throw NativeError.invalid("预览帧没有可显示的图像")
        }
        return .video(decoded)
    }
    private func check(_ status: OSStatus) throws {
        guard status == noErr else { throw NativeError.invalid("预览解码失败（\(status)）") }
    }
    private func videoFormat(_ data: Data) throws -> CMFormatDescription {
        let bytes = [UInt8](data)
        guard bytes.count >= 7, bytes[0] == 1 else { throw NativeError.invalid("无效的视频配置") }
        let headerLength = Int32((bytes[4] & 3) + 1)
        guard [1, 2, 4].contains(headerLength) else { throw NativeError.invalid("无效的视频配置") }
        var offset = 6
        var sets: [NSData] = []
        func appendSets(_ count: Int) throws {
            guard count > 0 else { throw NativeError.invalid("视频配置缺少参数") }
            for _ in 0..<count {
                guard offset + 2 <= bytes.count else { throw NativeError.invalid("视频配置不完整") }
                let length = Int(bytes[offset]) << 8 | Int(bytes[offset + 1]); offset += 2
                guard length > 0, offset + length <= bytes.count else { throw NativeError.invalid("视频配置不完整") }
                sets.append(Data(bytes[offset..<(offset + length)]) as NSData); offset += length
            }
        }
        try appendSets(Int(bytes[5] & 31))
        guard offset < bytes.count else { throw NativeError.invalid("视频配置不完整") }
        let pictureSets = Int(bytes[offset]); offset += 1
        try appendSets(pictureSets)
        let pointers = sets.map { $0.bytes.assumingMemoryBound(to: UInt8.self) }
        let sizes = sets.map(\.length)
        var output: CMFormatDescription?
        try withExtendedLifetime(sets) {
            try pointers.withUnsafeBufferPointer { pointers in
                try sizes.withUnsafeBufferPointer { sizes in
                    try check(CMVideoFormatDescriptionCreateFromH264ParameterSets(allocator: kCFAllocatorDefault,
                        parameterSetCount: sets.count, parameterSetPointers: pointers.baseAddress!, parameterSetSizes: sizes.baseAddress!,
                        nalUnitHeaderLength: headerLength, formatDescriptionOut: &output))
                }
            }
        }
        guard let output else { throw NativeError.invalid("无法读取视频配置") }
        return output
    }
}

private struct PreviewSurfaceView: UIViewRepresentable {
    let surface: PreviewSurface
    func makeUIView(context: Context) -> PreviewSurface { surface }
    func updateUIView(_ uiView: PreviewSurface, context: Context) {}
}

/// UIKit owns the changing pixels; no frame update re-renders the SwiftUI transcript.
private final class PreviewSurface: UIScrollView, UIScrollViewDelegate {
    private let canvas = UIView()
    private let image = UIImageView()
    private let video = AVSampleBufferDisplayLayer()
    private var pixels = CGSize.zero
    private var viewport = CGSize.zero
    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .black; delegate = self
        showsHorizontalScrollIndicator = false; showsVerticalScrollIndicator = false
        bouncesZoom = true; canvas.backgroundColor = .black
        image.contentMode = .scaleAspectFit; video.videoGravity = .resizeAspect
        canvas.layer.addSublayer(video); canvas.addSubview(image); addSubview(canvas)
    }
    convenience init() { self.init(frame: .zero) }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    func viewForZooming(in scrollView: UIScrollView) -> UIView? { canvas }
    func scrollViewDidZoom(_ scrollView: UIScrollView) { centerCanvas() }
    override func layoutSubviews() {
        super.layoutSubviews()
        if viewport != bounds.size { viewport = bounds.size; fit() }
        centerCanvas()
    }
    func clear() {
        image.image = nil; video.flushAndRemoveImage()
        pixels = .zero
    }
    func present(_ frame: DecodedPreview, size: CGSize) throws {
        if pixels != size {
            minimumZoomScale = 1; maximumZoomScale = 4
            pixels = size; zoomScale = 1; canvas.bounds = CGRect(origin: .zero, size: size)
            image.frame = canvas.bounds; video.frame = canvas.bounds; fit()
        }
        switch frame {
        case .image(let pixels):
            video.isHidden = true; image.isHidden = false; image.image = UIImage(cgImage: pixels)
        case .video(let pixels):
            image.isHidden = true; video.isHidden = false
            var format: CMVideoFormatDescription?
            guard CMVideoFormatDescriptionCreateForImageBuffer(allocator: kCFAllocatorDefault, imageBuffer: pixels, formatDescriptionOut: &format) == noErr,
                  let format else { throw NativeError.invalid("无法显示预览帧") }
            var sample: CMSampleBuffer?
            var timing = CMSampleTimingInfo(duration: .invalid, presentationTimeStamp: .zero, decodeTimeStamp: .invalid)
            guard CMSampleBufferCreateReadyWithImageBuffer(allocator: kCFAllocatorDefault, imageBuffer: pixels,
                formatDescription: format, sampleTiming: &timing, sampleBufferOut: &sample) == noErr, let sample else {
                throw NativeError.invalid("无法显示预览帧")
            }
            if let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true) as? [NSMutableDictionary] {
                attachments.first?[kCMSampleAttachmentKey_DisplayImmediately] = true
            }
            // Decoded pixels can be replaced safely; compressed deltas were already consumed in order.
            if video.status == .failed || !video.isReadyForMoreMediaData { video.flush() }
            video.enqueue(sample)
        }
    }
    private func fit() {
        guard pixels.width > 0, pixels.height > 0, bounds.width > 0, bounds.height > 0 else { return }
        let relative = minimumZoomScale > 0 ? zoomScale / minimumZoomScale : 1
        let scale = min(bounds.width / pixels.width, bounds.height / pixels.height)
        minimumZoomScale = scale; maximumZoomScale = scale * 4
        zoomScale = min(maximumZoomScale, max(scale, scale * relative))
    }
    private func centerCanvas() {
        canvas.center = CGPoint(x: max(bounds.width, contentSize.width) / 2, y: max(bounds.height, contentSize.height) / 2)
    }
}
