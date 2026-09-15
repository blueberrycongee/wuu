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
    func update(_ next: TimelineScrollMetrics, following: Bool) -> (following: Bool, scrollToBottom: Bool) {
        let previous = metrics
        metrics = next
        let moved = next.interacting || (previous?.height == next.height && previous?.offset != next.offset)
        let follow = next.atBottom ? true : moved ? false : following
        return (follow, follow && !next.interacting && (previous?.height != next.height || previous?.viewport != next.viewport))
    }
}

/// Observe the scroll container, not a lazy bottom row that disappears offscreen.
struct TimelineScrollReader: UIViewRepresentable {
    let changed: (TimelineScrollMetrics) -> Void
    func makeUIView(context: Context) -> Observer {
        let view = Observer(); view.isUserInteractionEnabled = false; return view
    }
    func updateUIView(_ view: Observer, context: Context) { view.changed = changed; view.schedule() }
    static func dismantleUIView(_ view: Observer, coordinator: ()) { view.stop() }

    final class Observer: UIView {
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
            observations = [scroll.observe(\.contentOffset) { [weak self] _, _ in self?.schedule() },
                scroll.observe(\.contentSize) { [weak self] _, _ in self?.schedule() },
                scroll.observe(\.bounds) { [weak self] _, _ in self?.schedule() }]
        }
        private func report() {
            guard let scroll else { return }
            let value = TimelineScrollMetrics(offset: scroll.contentOffset.y,
                height: scroll.contentSize.height + scroll.adjustedContentInset.bottom,
                viewport: scroll.bounds.height, interacting: scroll.isTracking || scroll.isDragging || scroll.isDecelerating)
            guard last != value else { return }
            last = value; changed?(value)
        }
    }
}
