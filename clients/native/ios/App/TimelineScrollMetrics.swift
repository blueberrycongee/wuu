import SwiftUI

struct TimelineScrollMetrics: Equatable {
    let offset: CGFloat
    let height: CGFloat
    let viewport: CGFloat
    let interacting: Bool
    // The timeline is laid out from newest to oldest, then flipped for display.
    var atBottom: Bool { offset <= 48 }
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
        var viewport: CGFloat
        var rowEdge: CGFloat
        let edge: VerticalEdge
        var offset: CGFloat
        var translation: CGFloat
        var changed = false
        var stableFrames = 0
        var frames = 0
    }
    private var anchor: Anchor?
    var preservingPosition: Bool { anchor != nil }
    var beginRestoration: (() -> Void)?
    func preservePosition(at edge: VerticalEdge = .top) {
        guard let scroll else { return }
        let top = scroll.contentOffset.y + scroll.adjustedContentInset.top
        let visible = rows.filter { $0.value.window != nil }.map { ($0.key, $0.value.convert($0.value.bounds, to: scroll)) }
            .filter { $0.1.maxY > top && $0.1.minY < scroll.contentOffset.y + scroll.bounds.height }
            .max { $0.1.maxY < $1.1.maxY }
        guard let (id, frame) = visible else { return }
        // Coordinates run upward on screen. History pages retain the visual bottom
        // of a group that gains earlier members; live updates retain its visual top.
        let rowEdge = edge == .top ? frame.maxY : frame.minY
        anchor = Anchor(id: id, y: rowEdge - scroll.contentOffset.y, height: scroll.contentSize.height,
                        viewport: metrics?.viewport ?? scroll.bounds.height, rowEdge: rowEdge, edge: edge, offset: scroll.contentOffset.y,
                        translation: scroll.panGestureRecognizer.translation(in: scroll).y)
        beginRestoration?()
    }
    func cancelRestoration() { anchor = nil }
    func restorePosition() {
        guard let scroll, var anchor else { return }
        anchor.frames += 1
        // Retain the same distance from the visual top as the keyboard changes the viewport.
        anchor.y += scroll.bounds.height - anchor.viewport
        anchor.viewport = scroll.bounds.height
        let rowEdge = rows[anchor.id].flatMap { view -> CGFloat? in
            guard view.window != nil else { return nil }
            let frame = view.convert(view.bounds, to: scroll)
            return anchor.edge == .top ? frame.maxY : frame.minY
        }
        let geometryChanged = scroll.contentSize.height != anchor.height || (rowEdge != nil && rowEdge != anchor.rowEdge)
        // Continue the gesture while compensating only for inserted content.
        let translation = scroll.panGestureRecognizer.translation(in: scroll).y
        if scroll.isDragging {
            anchor.y += translation - anchor.translation
        } else if scroll.isDecelerating && !geometryChanged {
            anchor.y -= scroll.contentOffset.y - anchor.offset
        }
        anchor.translation = translation
        if geometryChanged { anchor.changed = true; anchor.stableFrames = 0 }
        else if anchor.changed && rowEdge != nil { anchor.stableFrames += 1 }
        // An unmounted row has no reliable position; never substitute a lazy height estimate.
        let offset = rowEdge.map { $0 - anchor.y } ?? scroll.contentOffset.y
        let maximum = max(-scroll.adjustedContentInset.top, scroll.contentSize.height - scroll.bounds.height + scroll.adjustedContentInset.bottom)
        let corrected = min(maximum, max(-scroll.adjustedContentInset.top, offset))
        if abs(corrected - scroll.contentOffset.y) > 0.5 {
            scroll.setContentOffset(CGPoint(x: scroll.contentOffset.x, y: corrected), animated: false)
        }
        anchor.offset = scroll.contentOffset.y
        anchor.height = scroll.contentSize.height
        if let rowEdge { anchor.rowEdge = rowEdge }
        // Lazy layout can refine its estimates over multiple frames. Bound recovery when
        // the anchor disappears entirely (for example, when switching conversations).
        self.anchor = anchor.stableFrames >= 3 || anchor.frames >= 30 ? nil : anchor
    }
    func scrollToBottom() {
        guard let scroll else { return }
        let bottom = -scroll.adjustedContentInset.top
        if abs(scroll.contentOffset.y - bottom) > 0.5 {
            scroll.setContentOffset(CGPoint(x: scroll.contentOffset.x, y: bottom), animated: false)
        }
    }
    func update(_ next: TimelineScrollMetrics, following: Bool) -> (following: Bool, scrollToBottom: Bool) {
        let previous = metrics
        metrics = next
        if preservingPosition { return (false, false) }
        // A viewport resize is not a scrolling gesture. Stable-geometry offsets
        // also cover accessibility scrolling, which does not use a pan gesture.
        let moved = next.interacting || (previous?.height == next.height && previous?.viewport == next.viewport && previous?.offset != next.offset)
        let follow = next.atBottom ? true : moved ? false : following
        return (follow, follow && !next.interacting && (previous?.height != next.height || previous?.viewport != next.viewport || !next.atBottom))
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
            if state?.preservingPosition != true {
                restoration?.invalidate(); restoration = nil
                // Following must resume at the latest edge even when the final geometry is unchanged.
                last = nil
            }
            report()
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
            // Remove when UIKit's edge effects respect a flipped scroll transform;
            // iOS 26 otherwise blurs the entire transcript.
            if #available(iOS 26.0, *) {
                scroll.topEdgeEffect.isHidden = true
                scroll.bottomEdgeEffect.isHidden = true
            }
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
            let value = TimelineScrollMetrics(offset: scroll.contentOffset.y + scroll.adjustedContentInset.top,
                height: scroll.contentSize.height + scroll.adjustedContentInset.bottom,
                viewport: scroll.bounds.height, interacting: scroll.isTracking || scroll.isDragging || scroll.isDecelerating)
            guard last != value else { return }
            last = value; changed?(value)
        }
    }
}
