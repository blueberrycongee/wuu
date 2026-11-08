// Encodes a directory of numbered PNG frames into an H.264 MP4 with AVFoundation,
// so rendering the promo does not depend on a separately installed ffmpeg.
// An optional audio file (the score) is encoded to AAC alongside the frames.
// Usage: swift encode.swift <frames-dir> <output.mp4> <fps> [audio.wav]
import AVFoundation
import CoreGraphics
import Foundation
import ImageIO

let args = CommandLine.arguments
guard args.count == 4 || args.count == 5, let fps = Int32(args[3]) else {
  FileHandle.standardError.write("usage: encode.swift <frames-dir> <output.mp4> <fps> [audio.wav]\n".data(using: .utf8)!)
  exit(2)
}
let framesDir = URL(fileURLWithPath: args[1])
let output = URL(fileURLWithPath: args[2])
let audioURL = args.count == 5 ? URL(fileURLWithPath: args[4]) : nil
let frames = try FileManager.default.contentsOfDirectory(atPath: framesDir.path)
  .filter { $0.hasSuffix(".png") }.sorted()
guard let first = frames.first,
  let firstSource = CGImageSourceCreateWithURL(framesDir.appendingPathComponent(first) as CFURL, nil),
  let firstImage = CGImageSourceCreateImageAtIndex(firstSource, 0, nil)
else { fatalError("no frames in \(framesDir.path)") }
let width = firstImage.width, height = firstImage.height

try? FileManager.default.removeItem(at: output)
let writer = try AVAssetWriter(outputURL: output, fileType: .mp4)
let input = AVAssetWriterInput(mediaType: .video, outputSettings: [
  AVVideoCodecKey: AVVideoCodecType.h264,
  AVVideoWidthKey: width,
  AVVideoHeightKey: height,
  AVVideoCompressionPropertiesKey: [
    AVVideoAverageBitRateKey: width * height * Int(fps) / 5,
    AVVideoProfileLevelKey: AVVideoProfileLevelH264HighAutoLevel,
    AVVideoMaxKeyFrameIntervalKey: Int(fps) * 2,
  ],
  AVVideoColorPropertiesKey: [
    AVVideoColorPrimariesKey: AVVideoColorPrimaries_ITU_R_709_2,
    AVVideoTransferFunctionKey: AVVideoTransferFunction_ITU_R_709_2,
    AVVideoYCbCrMatrixKey: AVVideoYCbCrMatrix_ITU_R_709_2,
  ],
])
input.expectsMediaDataInRealTime = false
let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: input, sourcePixelBufferAttributes: [
  kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA,
  kCVPixelBufferWidthKey as String: width,
  kCVPixelBufferHeightKey as String: height,
])
writer.add(input)

// The score is read as PCM and encoded by the writer, which also records the
// encoder delay so the audio starts on frame zero.
var audioInput: AVAssetWriterInput?
var audioOutput: AVAssetReaderTrackOutput?
var reader: AVAssetReader?
if let audioURL {
  let asset = AVURLAsset(url: audioURL)
  guard let track = try await asset.loadTracks(withMediaType: .audio).first else { fatalError("no audio in \(audioURL.path)") }
  let r = try AVAssetReader(asset: asset)
  let out = AVAssetReaderTrackOutput(track: track, outputSettings: [
    AVFormatIDKey: kAudioFormatLinearPCM,
    AVLinearPCMBitDepthKey: 32,
    AVLinearPCMIsFloatKey: true,
    AVLinearPCMIsBigEndianKey: false,
    AVLinearPCMIsNonInterleaved: false,
  ])
  r.add(out)
  let a = AVAssetWriterInput(mediaType: .audio, outputSettings: [
    AVFormatIDKey: kAudioFormatMPEG4AAC,
    AVSampleRateKey: 48_000,
    AVNumberOfChannelsKey: 2,
    AVEncoderBitRateKey: 256_000,
  ])
  a.expectsMediaDataInRealTime = false
  writer.add(a)
  guard r.startReading() else { fatalError("cannot read \(audioURL.path): \(String(describing: r.error))") }
  (reader, audioOutput, audioInput) = (r, out, a)
}

writer.startWriting()
writer.startSession(atSourceTime: .zero)

// The writer interleaves the two tracks, so each is fed whenever it is ready.
let colorSpace = CGColorSpace(name: CGColorSpace.sRGB)!
let group = DispatchGroup()
var index = 0
group.enter()
input.requestMediaDataWhenReady(on: DispatchQueue(label: "video")) {
  while input.isReadyForMoreMediaData {
    if index == frames.count {
      input.markAsFinished()
      group.leave()
      return
    }
    autoreleasepool {
      let name = frames[index]
      guard let source = CGImageSourceCreateWithURL(framesDir.appendingPathComponent(name) as CFURL, nil),
        let image = CGImageSourceCreateImageAtIndex(source, 0, nil)
      else { fatalError("unreadable frame \(name)") }
      var buffer: CVPixelBuffer?
      CVPixelBufferPoolCreatePixelBuffer(nil, adaptor.pixelBufferPool!, &buffer)
      let pixels = buffer!
      CVPixelBufferLockBaseAddress(pixels, [])
      let context = CGContext(
        data: CVPixelBufferGetBaseAddress(pixels), width: width, height: height, bitsPerComponent: 8,
        bytesPerRow: CVPixelBufferGetBytesPerRow(pixels), space: colorSpace,
        bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue)!
      context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
      CVPixelBufferUnlockBaseAddress(pixels, [])
      adaptor.append(pixels, withPresentationTime: CMTime(value: CMTimeValue(index), timescale: fps))
    }
    index += 1
  }
}
if let audioInput, let audioOutput {
  group.enter()
  audioInput.requestMediaDataWhenReady(on: DispatchQueue(label: "audio")) {
    while audioInput.isReadyForMoreMediaData {
      guard let sample = audioOutput.copyNextSampleBuffer() else {
        audioInput.markAsFinished()
        group.leave()
        return
      }
      audioInput.append(sample)
    }
  }
}
await withCheckedContinuation { (finished: CheckedContinuation<Void, Never>) in
  group.notify(queue: .global()) { finished.resume() }
}
await writer.finishWriting()
if writer.status != .completed { fatalError("encoding failed: \(String(describing: writer.error))") }
if let reader, reader.status == .failed { fatalError("reading audio failed: \(String(describing: reader.error))") }
print("Encoded \(frames.count) frames at \(width)x\(height)@\(fps)\(audioURL == nil ? "" : " with audio") to \(output.path)")
