import Foundation

/// Personal's lifted message retains its measured width; oversized content scrolls.
public struct PersonalMessageMenuLayout {
    public let message: CGRect
    public let tools: CGRect
    public init(source: CGRect, viewport: CGRect, toolsHeight: CGFloat, toolsWidth: CGFloat = 200, alignTrailing: Bool = false) {
        let safe = viewport.insetBy(dx: 16, dy: 12)
        let gap: CGFloat = 24
        let toolHeight = min(toolsHeight, max(44, safe.height - 68))
        let height = min(source.height, max(1, safe.height - toolHeight - gap))
        let y = max(safe.minY, min(source.minY, safe.maxY - height - gap - toolHeight))
        let width = min(source.width, safe.width)
        let x = max(safe.minX, min(source.minX, safe.maxX - width))
        message = CGRect(x: x, y: y, width: width, height: height)
        let menuWidth = min(toolsWidth, safe.width)
        let menuX = max(safe.minX, min(alignTrailing ? message.maxX - menuWidth : message.minX, safe.maxX - menuWidth))
        tools = CGRect(x: menuX, y: y + height + gap, width: menuWidth, height: toolHeight)
    }
}
