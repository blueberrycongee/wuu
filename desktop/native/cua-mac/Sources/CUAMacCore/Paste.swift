import AppKit
import Darwin

// Serialize clipboard ownership across helper processes. Restore only our own
// write: a user copy or an application clipboard update always wins.
func withPasteContent<T>(_ text: String, format: PasteFormat, body: () throws -> T) throws -> T {
    let item = NSPasteboardItem()
    switch format {
    case .text:
        item.setString(text, forType: .string)
    case .markdown:
        let rich = try NSAttributedString(markdown: Data(text.utf8), options: .init())
        item.setString(rich.string, forType: .string)
        item.setString(text, forType: NSPasteboard.PasteboardType("net.daringfireball.markdown"))
        item.setData(try rich.data(from: NSRange(location: 0, length: rich.length),
            documentAttributes: [.documentType: NSAttributedString.DocumentType.rtf]), forType: .rtf)
    case .html:
        let rich = try NSAttributedString(data: Data(text.utf8),
            options: [.documentType: NSAttributedString.DocumentType.html,
                      .characterEncoding: String.Encoding.utf8.rawValue], documentAttributes: nil)
        item.setString(rich.string, forType: .string)
        item.setString(text, forType: .html)
    }
    return try withInputResourceLock(path: "/tmp/wuu-cua-clipboard-\(getuid()).lock") {
        try ComputerExecution.checkpoint()
        let board = NSPasteboard.general
        let original = (board.pasteboardItems ?? []).map { source in
            let copy = NSPasteboardItem()
            for type in source.types {
                if let data = source.data(forType: type) { copy.setData(data, forType: type) }
            }
            return copy
        }
        board.clearContents()
        var ownedRevision = board.changeCount
        defer {
            if board.changeCount == ownedRevision {
                board.clearContents()
                if !original.isEmpty { board.writeObjects(original) }
            }
        }
        guard board.writeObjects([item]) else {
            throw ComputerError.operationFailed("could not write paste content to clipboard")
        }
        ownedRevision = board.changeCount
        return try body()
    }
}
