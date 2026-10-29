import UIKit
import Social
import UniformTypeIdentifiers
import WuuCore

/// This extension only deposits a bounded local envelope. The owner later reviews
/// it in the authenticated main chat. It never opens URLs or holds credentials.
final class ShareViewController: SLComposeServiceViewController {
 private var posting = false
 override func isContentValid() -> Bool { !posting && (contentText ?? "").utf8.count <= 60000 }
 override func didSelectPost() {
  guard !posting else { return }; posting = true
  Task { @MainActor in
   do {
    let inbox = try PersonalShareInbox.system(), lease = try inbox.begin()
    let providers = (extensionContext?.inputItems as? [NSExtensionItem] ?? []).flatMap { $0.attachments ?? [] }
    guard providers.count <= 8 else { throw NativeError.invalid("分享项目过多") }
    var text = contentText ?? "", files: [InputAttachment] = []
    for provider in providers {
     if provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
      let value = try await item(provider, type: UTType.url.identifier)
      guard let url = value as? URL, ["https", "http"].contains(url.scheme ?? ""), url.user == nil, url.password == nil else { throw NativeError.invalid("只支持网页链接") }
      text += "\n" + url.absoluteString
     } else if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
      let value = try await item(provider, type: UTType.plainText.identifier)
      guard let shared = value as? String else { throw NativeError.invalid("无效分享文字") }
      text += "\n" + shared
     } else {
      let types: [(UTType, String)] = [(.jpeg,"image/jpeg"),(.png,"image/png"),(.gif,"image/gif"),(.webP,"image/webp"),(.pdf,"application/pdf")]
      guard let type = types.first(where: { provider.hasItemConformingToTypeIdentifier($0.0.identifier) }) else { throw NativeError.invalid("附件支持图片和 PDF") }
      let (name, data) = try await file(provider, type: type.0.identifier)
      files.append(try InputAttachment(filename: name, mediaType: type.1, data: data))
     }
     guard text.utf8.count <= 60000 else { throw NativeError.invalid("文字超过 60 KB") }
     try InputAttachment.validate(files, text: text)
    }
    _ = try inbox.append(text: text, attachments: files, expectedLease: lease)
    extensionContext?.completeRequest(returningItems: [], completionHandler: nil)
   } catch {
    let alert = UIAlertController(title: "未接收分享", message: "请先在 Wuu Personal 主聊天启用分享接收。每次最多 4 个图片/PDF、合计 3 MB；分享后返回主聊天接收。", preferredStyle: .alert)
    alert.addAction(UIAlertAction(title: "关闭", style: .cancel) { _ in self.extensionContext?.cancelRequest(withError: NSError(domain: "WuuShare", code: 1)) })
    present(alert, animated: true)
   }
  }
 }
 private func item(_ provider: NSItemProvider, type: String) async throws -> NSSecureCoding {
  try await withCheckedThrowingContinuation { continuation in
   provider.loadItem(forTypeIdentifier: type, options: nil) { value, error in
    if let error { continuation.resume(throwing: error) }
    else if let value { continuation.resume(returning: value) }
    else { continuation.resume(throwing: NativeError.invalid("无法读取分享内容")) }
   }
  }
 }
 private func file(_ provider: NSItemProvider, type: String) async throws -> (String, Data) {
  try await withCheckedThrowingContinuation { continuation in
   provider.loadFileRepresentation(forTypeIdentifier: type) { url, error in
    do {
     if let error { throw error }
     guard let url, url.isFileURL else { throw NativeError.invalid("无法读取分享附件") }
     // The system-provided temporary URL is valid only inside this callback.
     let handle = try FileHandle(forReadingFrom: url); defer { try? handle.close() }
     var data = Data()
     while data.count <= InputAttachment.maxBytes {
      let chunk = try handle.read(upToCount: min(8192, InputAttachment.maxBytes + 1 - data.count)) ?? Data()
      if chunk.isEmpty { break }
      data.append(chunk)
     }
     guard data.count <= InputAttachment.maxBytes else { throw NativeError.invalid("附件超过 3 MB") }
     continuation.resume(returning: (url.lastPathComponent, data))
    } catch { continuation.resume(throwing: error) }
   }
  }
 }
}
