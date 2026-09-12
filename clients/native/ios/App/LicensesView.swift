import SwiftUI

struct LicensesView: View {
    @State private var text = ""
    var body: some View {
        ScrollView { Text(verbatim: text).font(.footnote).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding() }
            .navigationTitle("开源许可").navigationBarTitleDisplayMode(.inline)
            .task {
                do {
                    text = try ["Notices", "Apache-2.0"].map { name in
                        guard let url = Bundle.main.url(forResource: name, withExtension: "txt", subdirectory: "licenses") else {
                            throw CocoaError(.fileNoSuchFile)
                        }
                        return try String(contentsOf: url, encoding: .utf8)
                    }.joined(separator: "\n\n")
                } catch { text = "无法读取许可文件：" + error.localizedDescription }
            }
    }
}
