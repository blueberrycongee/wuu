// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "wuu-app-context",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "wuu-app-context", targets: ["AppContextCapture"])],
    targets: [.executableTarget(name: "AppContextCapture")]
)
