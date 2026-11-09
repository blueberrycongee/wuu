// Disposable AppKit target for the Computer Use README capture. It reads a
// synthetic delivery note, exposes native controls, and writes the note back
// when Save is pressed. No user application or document is involved.
import AppKit

let arguments = CommandLine.arguments
let zh = arguments.count > 1 && arguments[1] == "zh"
let notePath = arguments.count > 2 ? arguments[2] : "/tmp/northwind-delivery.txt"
let oldNote = zh ? "送至工作室入口。" : "Deliver to the studio entrance."
let noteFileNote = (try? String(contentsOfFile: notePath, encoding: .utf8))?.trimmingCharacters(in: .whitespacesAndNewlines)
let initialNote = noteFileNote.flatMap { $0.isEmpty ? nil : $0 } ?? oldNote

func L(_ en: String, _ cn: String) -> String { zh ? cn : en }

let app = NSApplication.shared
app.setActivationPolicy(.regular)

final class InvoiceWindow: NSWindow {
    override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect }
}

let window = InvoiceWindow(
    contentRect: NSRect(x: 80, y: 150, width: 900, height: 620),
    styleMask: [.titled, .closable, .miniaturizable, .resizable],
    backing: .buffered,
    defer: false
)
window.title = L("Northwind Invoice", "Northwind 发票")
window.titleVisibility = .hidden
window.titlebarAppearsTransparent = true
window.isMovableByWindowBackground = true
window.isReleasedWhenClosed = false
window.backgroundColor = NSColor(calibratedRed: 0.965, green: 0.953, blue: 0.925, alpha: 1)
window.minSize = NSSize(width: 760, height: 540)
// The floating Wuu preview supplies its own controls; native traffic lights
// underneath the preview chrome read as a duplicate control cluster.
for kind in [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton] {
    window.standardWindowButton(kind)?.isHidden = true
}

let content = NSView(frame: NSRect(x: 0, y: 0, width: 900, height: 620))
content.wantsLayer = true
content.layer?.backgroundColor = NSColor(calibratedRed: 0.965, green: 0.953, blue: 0.925, alpha: 1).cgColor
window.contentView = content

let card = NSView(frame: NSRect(x: 38, y: 34, width: 824, height: 540))
card.wantsLayer = true
card.layer?.backgroundColor = NSColor.white.cgColor
card.layer?.cornerRadius = 14
card.layer?.borderWidth = 1
card.layer?.borderColor = NSColor(calibratedWhite: 0.88, alpha: 1).cgColor
card.layer?.shadowColor = NSColor.black.cgColor
card.layer?.shadowOpacity = 0.10
card.layer?.shadowRadius = 18
card.layer?.shadowOffset = NSSize(width: 0, height: -6)
content.addSubview(card)

func label(_ text: String, frame: NSRect, size: CGFloat, weight: NSFont.Weight = .regular, color: NSColor = NSColor(calibratedWhite: 0.14, alpha: 1), mono: Bool = false) -> NSTextField {
    let field = NSTextField(labelWithString: text)
    field.frame = frame
    field.font = mono ? NSFont.monospacedSystemFont(ofSize: size, weight: weight) : NSFont.systemFont(ofSize: size, weight: weight)
    field.textColor = color
    field.lineBreakMode = .byTruncatingTail
    card.addSubview(field)
    return field
}

func rule(y: CGFloat) {
    let line = NSView(frame: NSRect(x: 36, y: y, width: 752, height: 1))
    line.wantsLayer = true
    line.layer?.backgroundColor = NSColor(calibratedWhite: 0.90, alpha: 1).cgColor
    card.addSubview(line)
}

let muted = NSColor(calibratedWhite: 0.46, alpha: 1)
let ink = NSColor(calibratedWhite: 0.12, alpha: 1)
let accent = NSColor(calibratedRed: 0.16, green: 0.34, blue: 0.27, alpha: 1)

_ = label(L("NORTHWIND PAPER CO.", "NORTHWIND 纸张公司"), frame: NSRect(x: 36, y: 486, width: 320, height: 16), size: 10.5, weight: .semibold, color: accent)
_ = label(L("INVOICE", "发票"), frame: NSRect(x: 646, y: 486, width: 142, height: 16), size: 10.5, weight: .semibold, color: muted)
_ = label("NW-2041", frame: NSRect(x: 36, y: 444, width: 420, height: 34), size: 26, weight: .semibold, color: ink)
_ = label(L("October paper order", "十月纸张订单"), frame: NSRect(x: 36, y: 418, width: 420, height: 20), size: 13, color: muted)

_ = label(L("BILLED TO", "客户"), frame: NSRect(x: 646, y: 452, width: 142, height: 14), size: 9.5, weight: .semibold, color: muted)
_ = label("Pocket Ledger", frame: NSRect(x: 646, y: 430, width: 142, height: 18), size: 13, weight: .medium, color: ink)
_ = label(L("Studio account", "工作室账户"), frame: NSRect(x: 646, y: 410, width: 142, height: 16), size: 11.5, color: muted)

_ = label(L("ISSUED", "开具日期"), frame: NSRect(x: 36, y: 384, width: 110, height: 14), size: 9.5, weight: .semibold, color: muted)
_ = label(L("October 2, 2026", "2026 年 10 月 2 日"), frame: NSRect(x: 36, y: 364, width: 180, height: 16), size: 12, color: ink)
_ = label(L("DUE", "到期日"), frame: NSRect(x: 230, y: 384, width: 110, height: 14), size: 9.5, weight: .semibold, color: muted)
_ = label(L("October 30, 2026", "2026 年 10 月 30 日"), frame: NSRect(x: 230, y: 364, width: 180, height: 16), size: 12, color: ink)

rule(y: 344)
_ = label(L("DESCRIPTION", "项目"), frame: NSRect(x: 36, y: 318, width: 420, height: 14), size: 9.5, weight: .semibold, color: muted)
_ = label(L("AMOUNT", "金额"), frame: NSRect(x: 636, y: 318, width: 152, height: 14), size: 9.5, weight: .semibold, color: muted)
_ = label(L("Recycled paper, A4", "再生纸，A4"), frame: NSRect(x: 36, y: 284, width: 430, height: 20), size: 13, color: ink)
_ = label("$4,200.00", frame: NSRect(x: 636, y: 284, width: 152, height: 20), size: 13, color: ink, mono: true)
_ = label(L("Delivery", "配送"), frame: NSRect(x: 36, y: 254, width: 430, height: 20), size: 13, color: ink)
_ = label("$80.00", frame: NSRect(x: 636, y: 254, width: 152, height: 20), size: 13, color: ink, mono: true)
rule(y: 232)
_ = label(L("Total", "合计"), frame: NSRect(x: 36, y: 196, width: 200, height: 24), size: 16, weight: .semibold, color: ink)
_ = label("$4,280.00", frame: NSRect(x: 636, y: 196, width: 152, height: 24), size: 16, weight: .semibold, color: ink, mono: true)
rule(y: 172)

_ = label(L("DELIVERY NOTE", "送货备注"), frame: NSRect(x: 36, y: 142, width: 220, height: 15), size: 9.5, weight: .semibold, color: muted)
_ = label(L("Saved with this invoice.", "随发票一起保存。"), frame: NSRect(x: 36, y: 124, width: 300, height: 16), size: 11, color: muted)

let noteFrame = NSRect(x: 36, y: 58, width: 566, height: 54)
let noteClip = NSView(frame: noteFrame)
noteClip.wantsLayer = true
noteClip.layer?.backgroundColor = NSColor(calibratedWhite: 0.985, alpha: 1).cgColor
noteClip.layer?.cornerRadius = 8
noteClip.layer?.borderWidth = 1
noteClip.layer?.borderColor = NSColor(calibratedWhite: 0.86, alpha: 1).cgColor
card.addSubview(noteClip)

let note = NSTextView(frame: NSRect(x: 13, y: 11, width: 540, height: 34))
note.string = initialNote
note.font = NSFont.systemFont(ofSize: 14)
note.textColor = ink
note.backgroundColor = .clear
note.drawsBackground = false
note.isRichText = false
note.isAutomaticQuoteSubstitutionEnabled = false
note.isAutomaticDashSubstitutionEnabled = false
note.isAutomaticTextReplacementEnabled = false
note.textContainerInset = NSSize(width: 0, height: 0)
note.setAccessibilityLabel(L("Delivery note", "送货备注"))
note.setAccessibilityIdentifier("northwind-delivery-note")
noteClip.addSubview(note)

let status = NSTextField(labelWithString: L("Saved", "已保存"))
status.frame = NSRect(x: 626, y: 66, width: 162, height: 18)
status.font = NSFont.systemFont(ofSize: 11.5, weight: .medium)
status.textColor = NSColor(calibratedRed: 0.18, green: 0.52, blue: 0.34, alpha: 1)
status.alignment = .right
status.setAccessibilityIdentifier("northwind-status")
card.addSubview(status)

let saveButton = NSButton(title: L("Save", "保存"), target: nil, action: nil)
saveButton.frame = NSRect(x: 626, y: 92, width: 162, height: 34)
saveButton.bezelStyle = .rounded
saveButton.controlSize = .large
saveButton.font = NSFont.systemFont(ofSize: 14, weight: .medium)
saveButton.keyEquivalent = ""
saveButton.isEnabled = false
saveButton.bezelColor = accent
saveButton.contentTintColor = .white
saveButton.setAccessibilityIdentifier("northwind-save")
saveButton.setAccessibilityHelp(L("Save the delivery note with this invoice.", "将送货备注保存到发票。"))
card.addSubview(saveButton)

final class Controller: NSObject, NSTextViewDelegate {
    let note: NSTextView
    let saveButton: NSButton
    let status: NSTextField
    let notePath: String
    let zh: Bool

    init(note: NSTextView, saveButton: NSButton, status: NSTextField, notePath: String, zh: Bool) {
        self.note = note
        self.saveButton = saveButton
        self.status = status
        self.notePath = notePath
        self.zh = zh
        super.init()
        note.delegate = self
        saveButton.target = self
        saveButton.action = #selector(saveNote)
    }

    func textDidChange(_ notification: Notification) {
        saveButton.isEnabled = true
        status.stringValue = zh ? "未保存" : "Unsaved changes"
        status.textColor = NSColor(calibratedRed: 0.69, green: 0.43, blue: 0.10, alpha: 1)
    }

    @objc func saveNote() {
        do {
            try (note.string + "\n").write(toFile: notePath, atomically: true, encoding: .utf8)
            saveButton.isEnabled = false
            status.stringValue = zh ? "已保存" : "Saved"
            status.textColor = NSColor(calibratedRed: 0.18, green: 0.52, blue: 0.34, alpha: 1)
        } catch {
            status.stringValue = zh ? "保存失败" : "Could not save"
            status.textColor = NSColor.systemRed
        }
    }
}

let controller = Controller(note: note, saveButton: saveButton, status: status, notePath: notePath, zh: zh)
withExtendedLifetime(controller) {
    window.makeKeyAndOrderFront(nil)
    app.activate(ignoringOtherApps: true)
    app.run()
}
