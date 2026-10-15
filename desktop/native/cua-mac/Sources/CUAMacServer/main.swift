import AppKit
import CUAMacCore
import Foundation
import Darwin

_ = NSApplication.shared
if CommandLine.arguments.count == 2, CommandLine.arguments[1] == "--agent-display" {
    NSApplication.shared.setActivationPolicy(.prohibited)
    runAgentDisplayDaemon()
}
if CommandLine.arguments.count == 3, CommandLine.arguments[1] == "--native-preview-host" {
    guard let parentProcessID = Int32(CommandLine.arguments[2]), parentProcessID > 0 else {
        FileHandle.standardError.write(Data("wuu-cua-mac native preview failed: invalid parent process\n".utf8))
        exit(2)
    }
    NSApplication.shared.setActivationPolicy(.prohibited)
    let host = NativePreviewHost(parentProcessID: parentProcessID)
    host.run()
    withExtendedLifetime(host) { NSApplication.shared.run() }
    exit(0)
}
if CommandLine.arguments.count >= 7, CommandLine.arguments[1] == "--native-preview" {
    let activityID = CommandLine.arguments[2]
    let target = CommandLine.arguments[3]
    guard let processID = Int32(CommandLine.arguments[4]), processID >= 0,
          let windowID = UInt32(CommandLine.arguments[5]),
          let parentProcessID = Int32(CommandLine.arguments[6]), parentProcessID > 0 else {
        FileHandle.standardError.write(Data("wuu-cua-mac native preview failed: invalid target identity\n".utf8))
        exit(2)
    }
    NSApplication.shared.setActivationPolicy(.prohibited)
    let host = NativePreviewHost(parentProcessID: parentProcessID, singleSession: NativePreviewConfiguration(
        activityID: activityID,
        target: target,
        processID: processID > 0 ? processID : nil,
        windowID: windowID > 0 ? windowID : nil,
        parentProcessID: parentProcessID
    ))
    host.run()
    withExtendedLifetime(host) { NSApplication.shared.run() }
    exit(0)
}
let requests = MCPRequestQueue(backend: MacComputerBackend())
let outputLock = NSLock()
let terminationSignals = [SIGTERM, SIGINT].map { code in
    signal(code, SIG_IGN)
    let source = DispatchSource.makeSignalSource(signal: code, queue: .global())
    source.setEventHandler { requests.shutdown { exit(0) } }
    source.resume()
    return source
}
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
        outputLock.lock()
        FileHandle.standardOutput.write(data)
        outputLock.unlock()
    }
}
