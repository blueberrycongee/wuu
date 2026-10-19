// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "wuu-cua-mac",
    platforms: [.macOS(.v13)],
    products: [
        .library(name: "CUAMacCore", targets: ["CUAMacCore"]),
        .executable(name: "wuu-cua-mac", targets: ["CUAMacServer"]),
        .executable(name: "cua-mac-protocol-tests", targets: ["CUAMacProtocolTests"]),
    ],
    targets: [
        .target(
            name: "CUAVirtualDisplay",
            path: "Sources/CUAVirtualDisplay",
            linkerSettings: [
                .linkedFramework("CoreGraphics"),
                .linkedFramework("Foundation"),
            ]
        ),
        .target(
            name: "CUAMacCore",
            dependencies: ["CUAVirtualDisplay"],
            path: "Sources/CUAMacCore",
            linkerSettings: [
                .linkedFramework("AppKit"),
                .linkedFramework("ApplicationServices"),
                .linkedFramework("CoreGraphics"),
                .linkedFramework("CoreImage"),
                .linkedFramework("ImageIO"),
                .linkedFramework("ScreenCaptureKit"),
            ]
        ),
        .executableTarget(
            name: "CUAMacServer",
            dependencies: ["CUAMacCore"],
            path: "Sources/CUAMacServer"
        ),
        .executableTarget(
            name: "CUAMacProtocolTests",
            dependencies: ["CUAMacCore"],
            path: "ProtocolTests",
            exclude: ["stdio-smoke.mjs"]
        ),
        .testTarget(
            name: "CUAMacCoreTests",
            dependencies: ["CUAMacCore"],
            path: "Tests/CUAMacCoreTests"
        ),
    ]
)
