import SwiftUI

struct TimelineScrollMetrics: Equatable {
    let offset: CGFloat
    let height: CGFloat
    let viewport: CGFloat
    let interacting: Bool
    var atBottom: Bool { offset + viewport >= height - 48 }
}

// Pixel offsets are bookkeeping, not observable view state.
@MainActor final class TimelineScrollState {
    var metrics: TimelineScrollMetrics?
    weak var scroll: UIScrollView?
    var rows: [String: UIView] = [:]
    private var anchor: (view: UIView, y: CGFloat, offset: CGFloat, height: CGFloat)?
    func preservePosition() {
        guard let scroll else { return }
        let top = scroll.contentOffset.y + scroll.adjustedContentInset.top
        let visible = rows.values.filter { $0.window != nil }.map { ($0, $0.convert($0.bounds, to: scroll)) }
            .filter { $0.1.maxY > top }.min { $0.1.minY < $1.1.minY }
        guard let (view, frame) = visible else { return }
        anchor = (view, frame.minY - scroll.contentOffset.y, scroll.contentOffset.y, scroll.contentSize.height)
    }
    func restorePosition() {
        guard let scroll, let anchor, scroll.contentSize.height != anchor.height else { return }
        self.anchor = nil
        // Keep the visible row at the same pixel offset, including partial rows. A lazy row
        // can be recycled during insertion; then the content-height delta supplies the offset.
        let offset = anchor.view.window != nil
            ? anchor.view.convert(anchor.view.bounds, to: scroll).minY - anchor.y
            : anchor.offset + scroll.contentSize.height - anchor.height
        let maximum = max(-scroll.adjustedContentInset.top, scroll.contentSize.height - scroll.bounds.height + scroll.adjustedContentInset.bottom)
        scroll.setContentOffset(CGPoint(x: scroll.contentOffset.x, y: min(maximum, max(-scroll.adjustedContentInset.top, offset))), animated: false)
    }
    func update(_ next: TimelineScrollMetrics, following: Bool) -> (following: Bool, scrollToBottom: Bool) {
        let previous = metrics
        metrics = next
        let moved = next.interacting || (previous?.height == next.height && previous?.offset != next.offset)
        let follow = next.atBottom ? true : moved ? false : following
        return (follow, follow && !next.interacting && (previous?.height != next.height || previous?.viewport != next.viewport))
    }
}

/// Geometry anchors stay outside SwiftUI state so dragging does not rebuild the transcript.
struct TimelineRowAnchor: UIViewRepresentable {
    let id: String
    let state: TimelineScrollState
    func makeCoordinator() -> Coordinator { Coordinator(id: id, state: state) }
    func makeUIView(context: Context) -> UIView {
        let view = UIView(); view.isUserInteractionEnabled = false
        state.rows[id] = view
        return view
    }
    func updateUIView(_ view: UIView, context: Context) {}
    static func dismantleUIView(_ view: UIView, coordinator: Coordinator) {
        if coordinator.state?.rows[coordinator.id] === view { coordinator.state?.rows.removeValue(forKey: coordinator.id) }
    }
    final class Coordinator {
        let id: String
        weak var state: TimelineScrollState?
        init(id: String, state: TimelineScrollState) { self.id = id; self.state = state }
    }
}

/// Observe the scroll container, not a lazy bottom row that disappears offscreen.
struct TimelineScrollReader: UIViewRepresentable {
    let state: TimelineScrollState
    let changed: (TimelineScrollMetrics) -> Void
    func makeUIView(context: Context) -> Observer {
        let view = Observer(); view.isUserInteractionEnabled = false; return view
    }
    func updateUIView(_ view: Observer, context: Context) { view.state = state; view.changed = changed; view.schedule() }
    static func dismantleUIView(_ view: Observer, coordinator: ()) { view.stop() }

    final class Observer: UIView {
        weak var state: TimelineScrollState?
        var changed: ((TimelineScrollMetrics) -> Void)?
        private weak var scroll: UIScrollView?
        private var observations: [NSKeyValueObservation] = []
        private var scheduled = false
        private var last: TimelineScrollMetrics?
        override func didMoveToWindow() { super.didMoveToWindow(); if window != nil { schedule() } else { stop() } }
        func stop() { observations.removeAll(); scroll = nil; last = nil }
        func schedule() {
            guard !scheduled else { return }
            scheduled = true
            DispatchQueue.main.async { [weak self] in
                guard let self else { return }
                self.scheduled = false
                guard self.window != nil else { return }
                if self.scroll == nil {
                    var ancestor = self.superview
                    while let view = ancestor {
                        if let scroll = view as? UIScrollView { self.attach(scroll); break }
                        ancestor = view.superview
                    }
                }
                self.report()
            }
        }
        private func attach(_ scroll: UIScrollView) {
            self.scroll = scroll
            state?.scroll = scroll
            observations = [scroll.observe(\.contentOffset) { [weak self] _, _ in self?.schedule() },
                scroll.observe(\.contentSize) { [weak self] _, _ in self?.schedule() },
                scroll.observe(\.bounds) { [weak self] _, _ in self?.schedule() }]
        }
        private func report() {
            guard let scroll else { return }
            state?.restorePosition()
            let value = TimelineScrollMetrics(offset: scroll.contentOffset.y,
                height: scroll.contentSize.height + scroll.adjustedContentInset.bottom,
                viewport: scroll.bounds.height, interacting: scroll.isTracking || scroll.isDragging || scroll.isDecelerating)
            guard last != value else { return }
            last = value; changed?(value)
        }
    }
}
