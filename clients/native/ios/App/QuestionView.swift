import SwiftUI
import WuuCore

struct QuestionView: View {
    @Bindable var model: AppModel
    let request: JSONValue
    @State private var selected: [String: Set<String>] = [:]
    @State private var custom: [String: String] = [:]
    @State private var submitting = false
    private var questions: [JSONValue] { request["questions"].array }
    private var valid: Bool {
        questions.allSatisfy { question in
            let id = question["id"].string ?? ""
            return !(selected[id] ?? []).isEmpty || !(custom[id] ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
    }
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                ForEach(questions.indices, id: \.self) { index in
                    let question = questions[index]
                    let id = question["id"].string ?? ""
                    Text(question["question"].string ?? "").font(.headline)
                    if let detail = question["detail"].string { Text(detail).font(.caption).textSelection(.enabled) }
                    ForEach(question["options"].array.indices, id: \.self) { optionIndex in
                        let option = question["options"].array[optionIndex]
                        let label = option["label"].string ?? ""
                        Button {
                            var values = selected[id] ?? []
                            if values.contains(label) { values.remove(label) }
                            else if question["multi_select"].bool { values.insert(label) }
                            else { values = [label] }
                            selected[id] = values
                        } label: {
                            HStack(alignment: .top) {
                                Image(systemName: (selected[id] ?? []).contains(label) ? "checkmark.circle.fill" : "circle")
                                VStack(alignment: .leading) {
                                    Text(label)
                                    if let detail = option["description"].string { Text(detail).font(.caption).foregroundStyle(.secondary) }
                                }
                            }.frame(maxWidth: .infinity, alignment: .leading)
                        }.buttonStyle(.bordered)
                    }
                    if question["allow_custom"].bool {
                        TextField("其他回答", text: Binding(get: { custom[id] ?? "" }, set: { custom[id] = $0 }), axis: .vertical)
                            .textFieldStyle(.roundedBorder)
                    }
                }
                HStack {
                    Button("取消请求", role: .destructive) { submit(nil) }
                    Spacer()
                    Button("提交") {
                        submit(questions.map { question in
                            let id = question["id"].string ?? ""
                            return ["id": .string(id), "selected": .array((selected[id] ?? []).sorted().map(JSONValue.string)), "custom": .string(custom[id] ?? "")]
                        })
                    }.buttonStyle(.borderedProminent).disabled(!valid)
                }
            }.padding()
        }.frame(maxHeight: 320).background(.secondary.opacity(0.08))
            .disabled(submitting || !model.connected)
    }
    private func submit(_ answers: [JSONValue]?) {
        submitting = true
        model.perform {
            defer { submitting = false }
            try await model.answerQuestion(request, answers: answers)
        }
    }
}
