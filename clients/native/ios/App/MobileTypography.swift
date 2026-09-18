import SwiftUI

private struct MobileTextSizeKey: EnvironmentKey {
    static let defaultValue: CGFloat = MobileTypography.baseTextSize
}

extension EnvironmentValues {
    var mobileTextSize: CGFloat {
        get { self[MobileTextSizeKey.self] }
        set { self[MobileTextSizeKey.self] = newValue }
    }
}

struct MobileTypography: ViewModifier {
    static let baseTextSize: CGFloat = 15
    static let baseCodeSize: CGFloat = 11
    @ScaledMetric(relativeTo: .body) private var textSize: CGFloat = Self.baseTextSize
    func body(content: Content) -> some View {
        content.environment(\.mobileTextSize, textSize).font(.system(size: textSize))
    }
}
