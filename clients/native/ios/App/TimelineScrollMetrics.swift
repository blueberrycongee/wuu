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
    private struct Anchor {
        let id: String
        var y: CGFloat
        var height: CGFloat
        var rowBottom: CGFloat
        var offset: CGFloat
        var translation: CGFloat
        var changed = false
        var stableFrames = 0
        var frames = 0
    }
    private var anchor: Anchor?
    var preservingPosition: Bool { anchor != nil }
    var beginRestoration: (() -> Void)?
    func preservePosition() {
        guard let scroll else { return }
        let top = scroll.contentOffset.y + scroll.adjustedContentInset.top
        let visible = rows.filter { $0.value.window != nil }.map { ($0.key, $0.value.convert($0.value.bounds, to: scroll)) }
            .filter { $0.1.maxY > top && $0.1.minY < scroll.contentOffset.y + scroll.bounds.height }
            .min { $0.1.minY < $1.1.minY }
        guard let (id, frame) = visible else { return }
        // The trailing edge survives a page that adds earlier members to a folded group.
        anchor = Anchor(id: id, y: frame.maxY - scroll.contentOffset.y, height: scroll.contentSize.height,
                        rowBottom: frame.maxY, offset: scroll.contentOffset.y,
                        translation: scroll.panGestureRecognizer.translation(in: scroll).y)
        beginRestoration?()
    }
    func cancelRestoration() { anchor = nil }
    func restorePosition() {
        guard let scroll, var anchor else { return }
        anchor.frames += 1
        let bottom = rows[anchor.id].flatMap { view in
            view.window != nil ? view.convert(view.bounds, to: scroll).maxY : nil
        }
        let geometryChanged = scroll.contentSize.height != anchor.height || (bottom != nil && bottom != anchor.rowBottom)
        // Continue the gesture while compensating only for inserted content.
        let translation = scroll.panGestureRecognizer.translation(in: scroll).y
        if scroll.isDragging {
            anchor.y += translation - anchor.translation
        } else if scroll.isDecelerating && !geometryChanged {
            anchor.y -= scroll.contentOffset.y - anchor.offset
        }
        anchor.translation = translation
        if geometryChanged { anchor.changed = true; anchor.stableFrames = 0 }
        else if anchor.changed && bottom != nil { anchor.stableFrames += 1 }
        let offset = bottom.map { $0 - anchor.y }
            ?? (scroll.contentOffset.y + scroll.contentSize.height - anchor.height)
        let maximum = max(-scroll.adjustedContentInset.top, scroll.contentSize.height - scroll.bounds.height + scroll.adjustedContentInset.bottom)
        let corrected = min(maximum, max(-scroll.adjustedContentInset.top, offset))
        if abs(corrected - scroll.contentOffset.y) > 0.5 {
            scroll.setContentOffset(CGPoint(x: scroll.contentOffset.x, y: corrected), animated: false)
        }
        anchor.offset = scroll.contentOffset.y
        anchor.height = scroll.contentSize.height
        if let bottom { anchor.rowBottom = bottom }
        // Lazy layout can refine its estimates over multiple frames. Bound recovery when
        // the anchor disappears entirely (for example, when switching conversations).
        self.anchor = anchor.stableFrames >= 3 || anchor.frames >= 30 ? nil : anchor
    }
    func update(_ next: TimelineScrollMetrics, following: Bool) -> (following: Bool, scrollToBottom: Bool) {
        let previous = metrics
        metrics = next
        if preservingPosition { return (false, false) }
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
    func updateUIView(_ view: UIView, context: Context) {
        let coordinator = context.coordinator
        if coordinator.id != id, state.rows[coordinator.id] === view { state.rows.removeValue(forKey: coordinator.id) }
        coordinator.id = id
        state.rows[id] = view
    }
    static func dismantleUIView(_ view: UIView, coordinator: Coordinator) {
        if coordinator.state?.rows[coordinator.id] === view { coordinator.state?.rows.removeValue(forKey: coordinator.id) }
    }
    final class Coordinator {
        var id: String
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
        private var restoration: CADisplayLink?
        override func didMoveToWindow() { super.didMoveToWindow(); if window != nil { schedule() } else { stop() } }
        func stop() {
            restoration?.invalidate(); restoration = nil
            state?.beginRestoration = nil
            state?.cancelRestoration()
            scroll?.panGestureRecognizer.removeTarget(self, action: #selector(panChanged))
            observations.removeAll(); scroll = nil; last = nil
        }
        @objc private func panChanged(_ gesture: UIPanGestureRecognizer) {
            if gesture.state == .began { state?.cancelRestoration() }
        }
        @objc private func restoreFrame() {
            scroll?.superview?.layoutIfNeeded()
            state?.restorePosition()
            report()
            if state?.preservingPosition != true { restoration?.invalidate(); restoration = nil }
        }
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
            scroll.panGestureRecognizer.addTarget(self, action: #selector(panChanged))
            state?.scroll = scroll
            state?.beginRestoration = { [weak self] in
                guard let self else { return }
                self.restoration?.invalidate()
                let link = CADisplayLink(target: self, selector: #selector(self.restoreFrame))
                self.restoration = link
                link.add(to: .main, forMode: .common)
            }
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
