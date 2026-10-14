@preconcurrency import CoreMedia
import CoreVideo
import Foundation
@preconcurrency import VideoToolbox

/// Accessed only on the preview encoding queue. A new capture or size starts a
/// new AVC configuration; frames are never reordered or buffered for lookahead.
final class PreviewEncoder {
    struct Output: Sendable {
        let data: Data
        let description: Data
        let codec: String
        let keyframe: Bool
        let configuration: UInt64
        let timestamp: Int64
    }

    private var session: VTCompressionSession?
    private var generation: UInt64 = 0
    private var width = 0
    private var height = 0
    private var configuration: UInt64 = 0
    private var lastTimestamp: Int64 = -1

    deinit { reset() }

    private func reset() {
        if let session { VTCompressionSessionInvalidate(session) }
        session = nil
        lastTimestamp = -1
    }

    func encode(_ image: CVPixelBuffer, generation: UInt64, timestamp: CMTime, keyframe: Bool,
                completion: @escaping @Sendable (Result<Output?, Error>) -> Void) throws {
        let width = CVPixelBufferGetWidth(image), height = CVPixelBufferGetHeight(image)
        if session == nil || self.generation != generation || self.width != width || self.height != height {
            reset()
            var created: VTCompressionSession?
            try Self.checkStatus(VTCompressionSessionCreate(
                allocator: kCFAllocatorDefault, width: Int32(width), height: Int32(height),
                codecType: kCMVideoCodecType_H264,
                encoderSpecification: [
                    kVTVideoEncoderSpecification_EnableHardwareAcceleratedVideoEncoder: true,
                    kVTVideoEncoderSpecification_EnableLowLatencyRateControl: true,
                ] as CFDictionary,
                imageBufferAttributes: nil, compressedDataAllocator: nil,
                outputCallback: nil, refcon: nil, compressionSessionOut: &created
            ))
            guard let created else { throw ComputerError.operationFailed("native video encoder unavailable") }
            session = created
            let properties: [CFString: Any] = [
                kVTCompressionPropertyKey_RealTime: true,
                kVTCompressionPropertyKey_AllowFrameReordering: false,
                // Signal the no-reordering constraint in the bitstream too:
                // a decoder must output one frame without waiting for the next ACK.
                kVTCompressionPropertyKey_ProfileLevel: kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel,
                kVTCompressionPropertyKey_ExpectedFrameRate: 30,
                kVTCompressionPropertyKey_AverageBitRate: 4_000_000,
                kVTCompressionPropertyKey_MaxKeyFrameInterval: 60,
                kVTCompressionPropertyKey_MaxFrameDelayCount: 0,
            ]
            try Self.checkStatus(VTSessionSetProperties(created, propertyDictionary: properties as CFDictionary))
            try Self.checkStatus(VTCompressionSessionPrepareToEncodeFrames(created))
            self.generation = generation
            self.width = width
            self.height = height
            configuration += 1
        }
        let session = session!
        // Recovery may encode the last captured image again while the app is idle.
        // VideoToolbox requires strictly increasing presentation timestamps.
        let micros = max(lastTimestamp + 1, CMTimeConvertScale(timestamp, timescale: 1_000_000, method: .default).value)
        lastTimestamp = micros
        let configuration = configuration
        try Self.checkStatus(VTCompressionSessionEncodeFrame(
            session, imageBuffer: image, presentationTimeStamp: CMTime(value: micros, timescale: 1_000_000),
            duration: .invalid,
            frameProperties: keyframe ? [kVTEncodeFrameOptionKey_ForceKeyFrame: true] as CFDictionary : nil,
            infoFlagsOut: nil
        ) { status, flags, sample in
            do {
                try Self.checkStatus(status)
                if flags.contains(.frameDropped) { completion(.success(nil)); return }
                guard let sample,
                      let buffer = CMSampleBufferGetDataBuffer(sample),
                      let format = CMSampleBufferGetFormatDescription(sample),
                      let atoms = CMFormatDescriptionGetExtension(format, extensionKey: kCMFormatDescriptionExtension_SampleDescriptionExtensionAtoms) as? [String: Any],
                      let description = atoms["avcC"] as? Data, description.count >= 4 else {
                    throw ComputerError.operationFailed("native video encoder produced no AVC sample")
                }
                let length = CMBlockBufferGetDataLength(buffer)
                guard length > 0, length <= 3_000_000 else {
                    throw ComputerError.operationFailed("native video sample exceeds the size limit")
                }
                var data = Data(count: length)
                let copied = data.withUnsafeMutableBytes {
                    CMBlockBufferCopyDataBytes(buffer, atOffset: 0, dataLength: length, destination: $0.baseAddress!)
                }
                try Self.checkStatus(copied)
                let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: false) as? [[CFString: Any]]
                let keyframe = attachments?.first?[kCMSampleAttachmentKey_NotSync] as? Bool != true
                let codec = "avc1." + description[1...3].map { String(format: "%02X", $0) }.joined()
                completion(.success(Output(data: data, description: description, codec: codec,
                                           keyframe: keyframe, configuration: configuration, timestamp: micros)))
            } catch { completion(.failure(error)) }
        })
    }

    private static func checkStatus(_ status: OSStatus) throws {
        guard status == noErr else { throw ComputerError.operationFailed("native video encoding failed (\(status))") }
    }
}
