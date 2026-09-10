import Foundation
import CoreGraphics
import CoreImage
import ImageIO

// Package the approved artwork without changing its composition. iOS applies
// its own mask; desktop and legacy Android launchers need baked-in corners.
let sourceURL = URL(fileURLWithPath: CommandLine.arguments[1])
let outputURL = URL(fileURLWithPath: CommandLine.arguments[2], isDirectory: true)
guard let source = CGImageSourceCreateWithURL(sourceURL as CFURL, nil),
      let artwork = CGImageSourceCreateImageAtIndex(source, 0, nil),
      artwork.width == artwork.height
else { fatalError("App icon source must be a square image") }

// Adaptive launchers show the central 72dp of a 108dp layer. Extend edge
// pixels into the motion margin so the visible crop retains the full artwork.
let original = CIImage(cgImage: artwork)
let margin = CGFloat(artwork.width) / 4
guard let adaptive = CIContext().createCGImage(
    original.clampedToExtent(),
    from: original.extent.insetBy(dx: -margin, dy: -margin)
) else { fatalError("Cannot extend adaptive icon edges") }

func render(_ image: CGImage, size: Int, style: String, name: String) {
    let transparent = style == "desktop" || style == "round"
    guard let context = CGContext(
        data: nil, width: size, height: size, bitsPerComponent: 8,
        bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: (transparent ? CGImageAlphaInfo.premultipliedLast : .noneSkipLast).rawValue
    ) else { fatalError("Cannot create icon bitmap") }
    context.interpolationQuality = .high
    let bounds = CGRect(x: 0, y: 0, width: size, height: size)
    var frame = bounds
    if style == "desktop" {
        frame = bounds.insetBy(dx: CGFloat(size) / 16, dy: CGFloat(size) / 16)
        let radius = frame.width * 0.22
        context.addPath(CGPath(roundedRect: frame, cornerWidth: radius, cornerHeight: radius, transform: nil))
        context.clip()
    } else if style == "round" {
        context.addEllipse(in: bounds)
        context.clip()
    }
    context.draw(image, in: frame)
    guard let bitmap = context.makeImage(),
          let destination = CGImageDestinationCreateWithURL(
            outputURL.appendingPathComponent(name) as CFURL, "public.png" as CFString, 1, nil
          )
    else { fatalError("Cannot encode icon") }
    CGImageDestinationAddImage(destination, bitmap, nil)
    guard CGImageDestinationFinalize(destination) else { fatalError("Cannot write icon") }
}

for size in [16, 24, 32, 48, 64, 128, 256, 512, 1024] {
    render(artwork, size: size, style: "desktop", name: "desktop-\(size).png")
}
render(artwork, size: 1024, style: "full", name: "mobile.png")
render(adaptive, size: 1024, style: "full", name: "adaptive.png")
for (density, scale) in [("mdpi", 1.0), ("hdpi", 1.5), ("xhdpi", 2.0), ("xxhdpi", 3.0), ("xxxhdpi", 4.0)] {
    render(artwork, size: Int(48 * scale), style: "full", name: "\(density)-ic_launcher.png")
    render(artwork, size: Int(48 * scale), style: "round", name: "\(density)-ic_launcher_round.png")
    render(adaptive, size: Int(108 * scale), style: "full", name: "\(density)-ic_launcher_foreground.png")
}
