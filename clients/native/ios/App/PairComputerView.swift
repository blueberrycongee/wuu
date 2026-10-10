import SwiftUI
import AVFoundation
import VisionKit

/// Pairs this phone with a computer from the one-time code Wuu shows on that computer.
/// The pasteboard is read only through the system paste control, never automatically.
struct PairComputerView: View {
    @Bindable var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var link: String
    @State private var task: Task<Void, Never>?
    @State private var failure: String?
    @State private var scanning = false
    @State private var cameraDenied = false
    @FocusState private var focused: Bool
    // False in the simulator and on devices without the required camera hardware.
    private var canScan: Bool { DataScannerViewController.isSupported }
    init(model: AppModel, link: String) {
        self.model = model
        _link = State(initialValue: link)
    }
    // A cancelled attempt may still be closing its connection in the model.
    private var busy: Bool { task != nil || model.pairingBusy }
    private var trimmed: String { link.trimmingCharacters(in: .whitespacesAndNewlines) }
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    Text("在电脑上打开 Wuu，进入「设置 › 手机访问」，选择「显示配对二维码」。")
                    if cameraDenied {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("Wuu 没有相机权限。可以在系统设置中允许，或在电脑上复制配对链接后粘贴到下方。")
                                .foregroundStyle(.secondary)
                            Button("打开设置") {
                                if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                            }.frame(minHeight: 44)
                        }.transition(.opacity)
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        Text(canScan ? "或粘贴配对链接" : "粘贴配对链接").font(.subheadline).foregroundStyle(.secondary)
                        TextField("wuu://pair?…", text: $link, axis: .vertical)
                            .lineLimit(2...5)
                            .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                            .submitLabel(.go).onSubmit(connect)
                            .focused($focused)
                            .padding(12)
                            .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
                            .accessibilityLabel("配对链接")
                            .disabled(busy)
                        HStack {
                            PasteButton(payloadType: String.self) { values in
                                guard let value = values.first else { return }
                                link = value; failure = nil
                            }.labelStyle(.titleAndIcon).buttonBorderShape(.capsule).disabled(busy)
                            Spacer()
                            if !link.isEmpty && !busy {
                                Button("清除") { link = ""; failure = nil; focused = true }.frame(minHeight: 44)
                            }
                        }
                    }
                    if let failure {
                        Label(failure, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red).textSelection(.enabled)
                            .transition(.opacity)
                    }
                    Text("配对后，这台手机可以控制这台电脑上的 Wuu：新建会话、发送指令，并接收回复和图片。只配对你信任的设备。配对码会过期，失败时请在电脑上重新生成。")
                        .font(.footnote).foregroundStyle(.secondary)
                }
                .padding(20)
                .animation(chromeAnimation(reduceMotion), value: failure)
                .animation(chromeAnimation(reduceMotion), value: cameraDenied)
            }
            .scrollDismissesKeyboard(.interactively)
            .safeAreaInset(edge: .bottom) { actionBar }
            .navigationTitle("连接电脑").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
            }
            .onChange(of: link) { _, _ in if !busy { failure = nil } }
            .sheet(isPresented: $scanning) {
                ScanPairingCodeView { value in link = value; connect() }
            }
        }
        // Dismissing abandons the wait; the computer's code stays valid until it expires.
        .onDisappear { task?.cancel() }
    }
    @ViewBuilder private var actionBar: some View {
        Group {
            if busy {
                HStack(spacing: 12) {
                    ProgressView()
                    Text("正在连接电脑…").foregroundStyle(.secondary)
                    Spacer()
                    Button("停止") { task?.cancel() }.frame(minHeight: 44)
                }
                .accessibilityElement(children: .contain)
            } else if canScan && trimmed.isEmpty {
                Button(action: scan) {
                    Label("扫描二维码", systemImage: "qrcode.viewfinder").frame(maxWidth: .infinity, minHeight: 44)
                }.mobilePrimaryAction().controlSize(.large)
            } else {
                Button(action: connect) {
                    Text(failure == nil ? "连接" : "重试").frame(maxWidth: .infinity, minHeight: 44)
                }.mobilePrimaryAction().controlSize(.large).disabled(trimmed.isEmpty)
            }
        }
        .padding(.horizontal, 20).padding(.vertical, 12)
        .background(Color(uiColor: .systemBackground))
    }
    private func scan() {
        focused = false
        Task {
            switch AVCaptureDevice.authorizationStatus(for: .video) {
            case .authorized: scanning = true
            case .notDetermined:
                if await AVCaptureDevice.requestAccess(for: .video) { scanning = true } else { cameraDenied = true }
            default: cameraDenied = true
            }
        }
    }
    private func connect() {
        guard !busy, !trimmed.isEmpty else { return }
        failure = nil; focused = false
        let input = trimmed
        task = Task {
            defer { task = nil }
            do {
                try await model.pairComputer(input)
                Haptics.success()
                dismiss()
            } catch is CancellationError {
            } catch {
                guard !Task.isCancelled else { return }
                failure = error.localizedDescription
                Haptics.failure()
            }
        }
    }
}

/// Live camera scan of the pairing QR code. Callers check camera permission and support first.
private struct ScanPairingCodeView: View {
    var found: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var notPairingCode = false
    @State private var failure: String?
    var body: some View {
        NavigationStack {
            Group {
                if let failure {
                    ContentUnavailableView("无法使用相机", systemImage: "camera", description: Text(failure + "\n可以返回并粘贴配对链接。"))
                } else {
                    QRScanner { value in
                        if value.hasPrefix("wuu://pair?") || (value.hasPrefix("https://") && value.contains("#pair=")) {
                            found(value); dismiss()
                        } else if !notPairingCode {
                            notPairingCode = true; Haptics.failure()
                        }
                    } failed: { failure = $0.localizedDescription }
                    .ignoresSafeArea(edges: .bottom)
                    .overlay(alignment: .bottom) {
                        Text(notPairingCode ? "这不是 Wuu 配对二维码" : "对准电脑上显示的配对二维码")
                            .padding(.horizontal, 16).padding(.vertical, 10)
                            .background(.regularMaterial, in: Capsule())
                            .padding(.bottom, 32)
                            .accessibilityAddTraits(.updatesFrequently)
                    }
                }
            }
            .navigationTitle("扫描二维码").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } } }
        }
    }
}

private struct QRScanner: UIViewControllerRepresentable {
    var found: (String) -> Void
    var failed: (Error) -> Void
    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], qualityLevel: .balanced,
            recognizesMultipleItems: false, isHighFrameRateTrackingEnabled: false, isPinchToZoomEnabled: true,
            isGuidanceEnabled: true, isHighlightingEnabled: true)
        scanner.delegate = context.coordinator
        return scanner
    }
    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {
        context.coordinator.found = found
        guard !scanner.isScanning else { return }
        do { try scanner.startScanning() } catch { DispatchQueue.main.async { failed(error) } }
    }
    static func dismantleUIViewController(_ scanner: DataScannerViewController, coordinator: Coordinator) {
        scanner.stopScanning()
    }
    @MainActor final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        var found: (String) -> Void = { _ in }
        func dataScanner(_ dataScanner: DataScannerViewController, didAdd addedItems: [RecognizedItem], allItems: [RecognizedItem]) {
            for item in addedItems {
                if case .barcode(let code) = item, let value = code.payloadStringValue { found(value); return }
            }
        }
    }
}
