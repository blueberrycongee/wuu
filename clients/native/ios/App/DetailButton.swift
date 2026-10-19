import SwiftUI

/// Keeps raw technical text, such as transport errors, out of the layout until asked for.
struct DetailButton: View {
    let detail: String
    var label = "查看详情"
    @State private var shown = false
    var body: some View {
        Button { shown = true } label: {
            Image(systemName: "info.circle").font(.system(size: 19)).frame(width: 44, height: 44)
        }
        .buttonStyle(.plain).foregroundStyle(.secondary)
        .accessibilityLabel(label)
        .popover(isPresented: $shown) {
            VStack(alignment: .leading, spacing: 12) {
                ScrollView {
                    Text(detail).font(.footnote).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }.frame(maxHeight: 280)
                Button("复制", systemImage: "doc.on.doc") {
                    UIPasteboard.general.string = detail; Haptics.tap(); shown = false
                }.frame(minHeight: 44)
            }
            .padding(16).frame(width: 300, alignment: .leading)
            .presentationCompactAdaptation(.popover)
        }
    }
}
