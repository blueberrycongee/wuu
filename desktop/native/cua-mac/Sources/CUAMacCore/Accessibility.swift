import ApplicationServices
import CoreGraphics
import Foundation

struct AXSnapshot {
    let text: String
    let elements: [Int: AXUIElement]
    let truncated: Bool

    init(text: String, elements: [Int: AXUIElement], truncated: Bool = false) {
        self.text = text
        self.elements = elements
        self.truncated = truncated
    }
}

struct AXElementDescriptor: Equatable {
    let role: String
    let title: String
    let description: String
    let frame: CGRect?
    let identifier: String
    let value: String?
}

final class AXSnapshotter {
    private(set) var elements: [Int: AXUIElement] = [:]
    private(set) var descriptors: [Int: AXElementDescriptor] = [:]
    private var truncated = false
    private let maxDepth = 14
    private let maxElements = 700

    func snapshot(application: AXUIElement) -> AXSnapshot {
        elements.removeAll(keepingCapacity: true)
        descriptors.removeAll(keepingCapacity: true)
        var lines: [String] = []
        var visited = Set<CFHashCode>()
        truncated = false
        walk(application, depth: 0, lines: &lines, visited: &visited)
        if truncated {
            lines.append("… accessibility tree truncated at \(maxElements) elements; use observe with root_element_id and snapshot_id to inspect a subtree.")
        }
        return AXSnapshot(text: lines.joined(separator: "\n"), elements: elements, truncated: truncated)
    }

    func element(id: Int) -> AXUIElement? {
        elements[id]
    }

    func clear() {
        elements.removeAll(keepingCapacity: true)
        descriptors.removeAll(keepingCapacity: true)
    }

    func isCurrent(id: Int) -> Bool {
        guard let element = elements[id], let saved = descriptors[id] else { return false }
        return descriptor(element) == saved
    }

    func descriptor(id: Int) -> AXElementDescriptor? { descriptors[id] }

    func uniqueElement(matching descriptor: AXElementDescriptor) -> AXUIElement? {
        let matches = descriptors.compactMap { id, candidate in candidate == descriptor ? elements[id] : nil }
        return matches.count == 1 ? matches[0] : nil
    }

    func uniqueElement(role: String?, title: String?, description: String?) -> AXUIElement? {
        let matches = descriptors.compactMap { id, candidate -> AXUIElement? in
            if let role, !role.isEmpty, candidate.role != role { return nil }
            if let title, !title.isEmpty, candidate.title != title { return nil }
            if let description, !description.isEmpty, candidate.description != description { return nil }
            return elements[id]
        }
        return matches.count == 1 ? matches[0] : nil
    }

    private func walk(_ element: AXUIElement, depth: Int, lines: inout [String], visited: inout Set<CFHashCode>) {
        guard elements.count < maxElements else { truncated = true; return }
        guard depth <= maxDepth else { truncated = true; return }
        let hash = CFHash(element)
        guard visited.insert(hash).inserted else { return }
        let id = elements.count
        elements[id] = element
        descriptors[id] = descriptor(element)
        lines.append(String(repeating: "  ", count: depth) + describe(element, id: id))
        for child in axElements(element, attribute: kAXChildrenAttribute as String) {
            walk(child, depth: depth + 1, lines: &lines, visited: &visited)
        }
    }

    private func descriptor(_ element: AXUIElement) -> AXElementDescriptor {
        AXElementDescriptor(
            role: axString(element, kAXRoleAttribute as String) ?? "",
            title: axString(element, kAXTitleAttribute as String) ?? "",
            description: axString(element, kAXDescriptionAttribute as String) ?? "",
            frame: axFrame(element),
            identifier: axString(element, kAXIdentifierAttribute as String) ?? "",
            value: axDisplayValue(element, kAXValueAttribute as String)
        )
    }

    private func describe(_ element: AXUIElement, id: Int) -> String {
        let role = axString(element, kAXRoleAttribute as String) ?? "AXUnknown"
        var parts = ["[\(id)]", role]
        appendQuoted(&parts, name: "subrole", value: axString(element, kAXSubroleAttribute as String))
        appendQuoted(&parts, name: "title", value: axString(element, kAXTitleAttribute as String))
        appendQuoted(&parts, name: "description", value: axString(element, kAXDescriptionAttribute as String))
        appendQuoted(&parts, name: "value", value: axDisplayValue(element, kAXValueAttribute as String))
        if let enabled = axBool(element, kAXEnabledAttribute as String) { parts.append("enabled=\(enabled)") }
        if let focused = axBool(element, kAXFocusedAttribute as String), focused { parts.append("focused=true") }
        if let frame = axFrame(element) {
            parts.append("frame=(\(Int(frame.origin.x)),\(Int(frame.origin.y)),\(Int(frame.width)),\(Int(frame.height)))")
        }
        let actions = axActions(element)
        if !actions.isEmpty {
            parts.append("actions=[\(actions.joined(separator: ","))]")
        }
        return parts.joined(separator: " ")
    }

    private func appendQuoted(_ parts: inout [String], name: String, value: String?) {
        guard let value, !value.isEmpty else { return }
        let compact = value.replacingOccurrences(of: "\n", with: "\\n")
        let clipped = compact.count > 300 ? String(compact.prefix(300)) + "…" : compact
        let escaped = clipped.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
        parts.append("\(name)=\"\(escaped)\"")
    }
}

func axValue(_ element: AXUIElement, _ attribute: String) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute as CFString, &value) == .success else { return nil }
    return value
}

func axString(_ element: AXUIElement, _ attribute: String) -> String? {
    axValue(element, attribute) as? String
}

func axBool(_ element: AXUIElement, _ attribute: String) -> Bool? {
    if let value = axValue(element, attribute) as? Bool { return value }
    if let value = axValue(element, attribute) as? NSNumber { return value.boolValue }
    return nil
}

func axDisplayValue(_ element: AXUIElement, _ attribute: String) -> String? {
    guard let value = axValue(element, attribute) else { return nil }
    if let string = value as? String { return string }
    if let number = value as? NSNumber { return number.stringValue }
    return nil
}

func axElements(_ element: AXUIElement, attribute: String) -> [AXUIElement] {
    axValue(element, attribute) as? [AXUIElement] ?? []
}

func axActions(_ element: AXUIElement) -> [String] {
    var actions: CFArray?
    guard AXUIElementCopyActionNames(element, &actions) == .success else { return [] }
    return actions as? [String] ?? []
}

func axFrame(_ element: AXUIElement) -> CGRect? {
    guard let positionValue = axValue(element, kAXPositionAttribute as String),
          let sizeValue = axValue(element, kAXSizeAttribute as String),
          CFGetTypeID(positionValue) == AXValueGetTypeID(),
          CFGetTypeID(sizeValue) == AXValueGetTypeID() else { return nil }
    var point = CGPoint.zero
    var size = CGSize.zero
    guard AXValueGetValue(positionValue as! AXValue, .cgPoint, &point),
          AXValueGetValue(sizeValue as! AXValue, .cgSize, &size) else { return nil }
    return CGRect(origin: point, size: size)
}

func setAXValue(_ element: AXUIElement, attribute: String, value: CFTypeRef) throws {
    try ComputerExecution.input()
    let error = AXUIElementSetAttributeValue(element, attribute as CFString, value)
    guard error == .success else {
        throw ComputerError.operationFailed("set \(attribute) failed with AX error \(error.rawValue)")
    }
}

func performAXAction(_ element: AXUIElement, action: String) throws {
    let available = axActions(element)
    guard available.contains(action) else {
        throw ComputerError.unsupported("AX element does not expose \(action); available actions: \(available.joined(separator: ", "))")
    }
    try ComputerExecution.input()
    let error = AXUIElementPerformAction(element, action as CFString)
    guard error == .success else {
        throw ComputerError.operationFailed("AX action \(action) failed with error \(error.rawValue)")
    }
}
