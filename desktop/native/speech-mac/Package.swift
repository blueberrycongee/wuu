// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "WuuSpeechMac",
    platforms: [.macOS(.v12)],
    products: [
        .executable(name: "wuu-speech-mac", targets: ["WuuSpeechMac"]),
    ],
    targets: [
        .executableTarget(
            name: "WuuSpeechMac",
            linkerSettings: [
                .linkedFramework("AVFoundation"),
                .linkedFramework("Speech"),
            ]
        ),
    ]
)
