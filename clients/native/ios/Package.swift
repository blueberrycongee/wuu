// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "WuuNative",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "WuuCore", targets: ["WuuCore"])],
    targets: [
        .target(name: "WuuCore"),
        .testTarget(name: "WuuCoreTests", dependencies: ["WuuCore"])
    ]
)
