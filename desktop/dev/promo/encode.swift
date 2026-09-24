// Encodes a directory of numbered PNG frames into an H.264 MP4 with AVFoundation,
// so rendering the promo does not depend on a separately installed ffmpeg.
// Usage: swift encode.swift <frames-dir> <output.mp4> <fps>
import AVFoundation
import CoreGraphics
import Foundation
import ImageIO

let args = CommandLine.arguments
guard args.count == 4, let fps = Int32(args[3]) else {
  FileHandle.standardError.write("usage: encode.swift <frames-dir> <output.mp4> <fps>\n".data(using: .utf8)!)
  exit(2)
}
let framesDir = URL(fileURLWithPath: args[1])
let output = URL(fileURLWithPath: args[2])
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
writer.startWriting()
writer.startSession(atSourceTime: .zero)

let colorSpace = CGColorSpace(name: CGColorSpace.sRGB)!
for (index, name) in frames.enumerated() {
  autoreleasepool {
    guard let source = CGImageSourceCreateWithURL(framesDir.appendingPathComponent(name) as CFURL, nil),
      let image = CGImageSourceCreateImageAtIndex(source, 0, nil)
    else { fatalError("unreadable frame \(name)") }
    while !input.isReadyForMoreMediaData { Thread.sleep(forTimeInterval: 0.002) }
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
}
input.markAsFinished()
let done = DispatchSemaphore(value: 0)
writer.finishWriting { done.signal() }
done.wait()
if writer.status != .completed { fatalError("encoding failed: \(String(describing: writer.error))") }
print("Encoded \(frames.count) frames at \(width)x\(height)@\(fps) to \(output.path)")
