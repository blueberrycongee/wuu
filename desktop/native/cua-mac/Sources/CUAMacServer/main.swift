import AppKit
import CUAMacCore
import Foundation

_ = NSApplication.shared
if CommandLine.arguments.count >= 8, CommandLine.arguments[1] == "--native-pip" {
    let activityID = CommandLine.arguments[2]
    let target = CommandLine.arguments[3]
    let values = CommandLine.arguments[4...7].compactMap(Double.init)
    guard values.count == 4 else {
        FileHandle.standardError.write(Data("wuu-cua-mac native PiP failed: invalid initial frame\n".utf8))
        exit(2)
    }
    let frame = CGRect(x: values[0], y: values[1], width: values[2], height: values[3])
    let processID = CommandLine.arguments.count > 8 ? Int32(CommandLine.arguments[8]) : nil
    let windowID = CommandLine.arguments.count > 9 ? UInt32(CommandLine.arguments[9]) : nil
    let parentProcessID = CommandLine.arguments.count > 10 ? Int32(CommandLine.arguments[10]) : nil
    NSApplication.shared.setActivationPolicy(.accessory)
    Task {
        do {
            try await runNativePiP(configuration: NativePiPConfiguration(
                activityID: activityID,
                target: target,
                frame: frame,
                processID: processID.flatMap { $0 > 0 ? $0 : nil },
                windowID: windowID.flatMap { $0 > 0 ? $0 : nil },
                parentProcessID: parentProcessID.flatMap { $0 > 0 ? $0 : nil }
            ))
            exit(0)
        } catch {
            let message = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            FileHandle.standardError.write(Data("wuu-cua-mac native PiP failed: \(message)\n".utf8))
            exit(1)
        }
    }
    NSApplication.shared.run()
    exit(0)
}
let requests = MCPRequestQueue(backend: MacComputerBackend())
DispatchQueue.global(qos: .userInitiated).async {
    while let line = readLine(strippingNewline: true) {
        guard !line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { continue }
        guard let data = line.data(using: .utf8),
              let request = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] else {
            DispatchQueue.main.async {
                writeResponse(["jsonrpc": "2.0", "id": NSNull(), "error": ["code": -32700, "message": "request must be a JSON object"]])
            }
            continue
        }
        requests.submit(request, reply: writeResponse)
    }
    requests.shutdown { exit(0) }
}
NSApplication.shared.run()

@Sendable func writeResponse(_ response: [String: Any]) {
    if var data = try? JSONSerialization.data(withJSONObject: response, options: [.sortedKeys]) {
        data.append(0x0A)
        FileHandle.standardOutput.write(data)
    }
}
