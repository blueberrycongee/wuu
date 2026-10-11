import SwiftUI

/// Keep paint ranges outside MarkdownUI's content-keyed blocks, which remount on append.
struct StreamingTextRevealState {
    static let duration = 0.3
    struct Chunk {
        let range: Range<Int>
        let born: TimeInterval
    }
    var text = ""
    var chunks: [Chunk] = []

    mutating func update(_ next: String, animated: Bool) {
        let now = Date.timeIntervalSinceReferenceDate
        guard animated else { text = next; chunks = []; return }
        // Compare graphemes before converting to Text.Layout's UTF-16 offsets.
        var prefix = 0
        for (old, new) in zip(text, next) {
            guard old == new else { break }
            prefix += String(new).utf16.count
        }
        chunks = chunks.compactMap { chunk in
            let end = min(chunk.range.upperBound, prefix)
            guard end > chunk.range.lowerBound, now - chunk.born < Self.duration else { return nil }
            return Chunk(range: chunk.range.lowerBound..<end, born: chunk.born)
        }
        let end = next.utf16.count
        if prefix < end { chunks.append(Chunk(range: prefix..<end, born: now)) }
        text = next
    }

    func chunks(in block: String) -> [Chunk] {
        guard !chunks.isEmpty, !block.isEmpty else { return [] }
        var segments = [(text: block, offset: 0)]
        if text.range(of: block) == nil {
            // cmark indents continuation lines inside lists in the document's plain text.
            // Match those lines separately without introducing indentation into glyph offsets.
            var offset = 0
            segments = block.components(separatedBy: "\n").map { line in
                defer { offset += line.utf16.count + 1 }
                return (line, offset)
            }
        }
        return segments.flatMap { segment -> [Chunk] in
            guard !segment.text.isEmpty, let match = text.range(of: segment.text) else { return [] }
            // Repeated identical blocks have no public identity in MarkdownUI. Keep them
            // visible rather than applying the newest block's fade to earlier copies.
            guard text.range(of: segment.text, range: match.upperBound..<text.endIndex) == nil else { return [] }
            let start = text[..<match.lowerBound].utf16.count
            let end = start + segment.text.utf16.count
            return chunks.compactMap { chunk in
                let lower = max(start, chunk.range.lowerBound), upper = min(end, chunk.range.upperBound)
                guard lower < upper else { return nil }
                return Chunk(range: (lower - start + segment.offset)..<(upper - start + segment.offset), born: chunk.born)
            }
        }
    }
}

/// Animation frames only paint already parsed and laid-out text.
struct StreamingTextReveal<Label: View>: View {
    let chunks: [StreamingTextRevealState.Chunk]
    let label: Label
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.scenePhase) private var scenePhase
    @State private var finished = false
    @State private var legacyOpacity = 0.0

    init(text: String, reveal: StreamingTextRevealState, @ViewBuilder label: () -> Label) {
        chunks = reveal.chunks(in: text); self.label = label()
    }

    var body: some View {
        let enabled = !reduceMotion && scenePhase == .active && !finished && !chunks.isEmpty
        Group {
            if #available(iOS 18.0, *) {
                TimelineView(.animation(minimumInterval: 1.0 / 60, paused: !enabled)) { timeline in
                    if enabled {
                        // Selectable Text uses a platform view that bypasses TextRenderer.
                        // Restore selection as soon as this block's last fade finishes.
                        label.textSelection(.disabled)
                            .textRenderer(RevealRenderer(chunks: chunks, time: timeline.date.timeIntervalSinceReferenceDate))
                    } else {
                        label
                    }
                }
            } else {
                // iOS 17 has no public textRenderer modifier; use a native block fade.
                label.opacity(enabled && chunks.first?.range.lowerBound == 0 ? legacyOpacity : 1)
                    .onAppear {
                        // Remounting a growing paragraph must continue its fade, never restart it.
                        let elapsed = Date.timeIntervalSinceReferenceDate - (chunks.first?.born ?? 0)
                        let progress = min(1, max(0, elapsed / StreamingTextRevealState.duration))
                        legacyOpacity = 1 - pow(1 - progress, 2)
                        withAnimation(.easeOut(duration: max(0, StreamingTextRevealState.duration - elapsed))) { legacyOpacity = 1 }
                    }
            }
        }
        .task(id: chunks.last?.born) {
            finished = false
            guard let born = chunks.last?.born else { return }
            let remaining = max(0, born + StreamingTextRevealState.duration - Date.timeIntervalSinceReferenceDate)
            do { try await Task.sleep(for: .seconds(remaining)) } catch { return }
            finished = true
        }
    }
}

@available(iOS 18.0, *)
private struct RevealRenderer: TextRenderer {
    let chunks: [StreamingTextRevealState.Chunk]
    let time: TimeInterval

    func draw(layout: Text.Layout, in context: inout GraphicsContext) {
        guard !chunks.isEmpty,
              let origin = layout.flatMap({ $0 }).flatMap(\.characterIndices).min() else {
            for line in layout { context.draw(line) }
            return
        }
        for line in layout {
            for run in line {
                let offsets = run.characterIndices.map { origin.distance(to: $0) }
                if !offsets.contains(where: { offset in chunks.contains { $0.range.contains(offset) } }) {
                    context.draw(run)
                    continue
                }
                for glyph in run {
                    let offsets = glyph.characterIndices.map { origin.distance(to: $0) }
                    let opacity = chunks.reduce(1.0) { value, chunk in
                        guard offsets.contains(where: chunk.range.contains) else { return value }
                        let progress = min(1, max(0, (time - chunk.born) / StreamingTextRevealState.duration))
                        return min(value, 1 - pow(1 - progress, 2))
                    }
                    var paint = context
                    paint.opacity *= opacity
                    paint.draw(glyph)
                }
            }
        }
    }
}
