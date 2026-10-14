import ApplicationServices
import CoreGraphics
import CryptoKit
import Foundation

struct AXSnapshot {
    let text: String
    let elements: [Int: AXUIElement]
    let truncated: Bool
    let nodes: [AXSnapshotNode]

    init(text: String, elements: [Int: AXUIElement], truncated: Bool = false, nodes: [AXSnapshotNode] = []) {
        self.text = text
        self.elements = elements
        self.truncated = truncated
        self.nodes = nodes
    }

    func difference(from previous: AXSnapshot, baseID: String) -> (structured: [String: Any], summary: [String]) {
        let old = Dictionary(uniqueKeysWithValues: previous.nodes.map { ($0.id, $0) })
        let currentIDs = Set(nodes.map(\.id))
        let added = nodes.filter { old[$0.id] == nil }
        let updated = nodes.filter { old[$0.id] != nil && old[$0.id] != $0 }
        let removed = previous.nodes.filter { !currentIDs.contains($0.id) }.map(\.id)
        let count = added.count + updated.count + removed.count
        let limit = 240
        let summary = added.map { "+ " + $0.text.trimmingCharacters(in: .whitespaces) } +
            updated.map { "~ " + $0.text.trimmingCharacters(in: .whitespaces) } + removed.map { "- [\($0)]" }
        return ([
            "base_snapshot_id": baseID,
            "added": added.prefix(limit).map(\.dictionary),
            "updated": updated.prefix(max(0, limit - added.count)).map(\.dictionary),
            "removed": Array(removed.prefix(max(0, limit - added.count - updated.count))),
            "total_changes": count,
            "truncated": count > limit,
            // Absence from a bounded traversal is not evidence of removal from the app.
            "complete": !truncated && !previous.truncated && count <= limit,
        ], Array(summary.prefix(limit)))
    }
}

struct AXSnapshotNode: Equatable {
    let id: Int
    let parentID: Int?
    let index: Int
    let attributes: [String: Any]
    let text: String

    var dictionary: [String: Any] {
        var result = attributes
        result["element_id"] = id
        result["parent_id"] = parentID.map { $0 as Any } ?? NSNull()
        result["index"] = index
        return result
    }

    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.id == rhs.id && lhs.parentID == rhs.parentID && lhs.index == rhs.index &&
            NSDictionary(dictionary: lhs.attributes).isEqual(to: rhs.attributes)
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
    private var nextID = 0
    private let maxDepth = 32
    private let maxElements = 700

    func snapshot(application: AXUIElement) -> AXSnapshot {
        let previous = Dictionary(grouping: elements.map { (id: $0.key, element: $0.value) }, by: { CFHash($0.element) })
        elements.removeAll(keepingCapacity: true)
        descriptors.removeAll(keepingCapacity: true)
        var nodes: [AXSnapshotNode] = []
        var visited: [CFHashCode: [AXUIElement]] = [:]
        truncated = false
        walk(application, parentID: nil, index: 0, depth: 0, nodes: &nodes, visited: &visited, previous: previous)
        var lines = nodes.map(\.text)
        if truncated {
            lines.append("… accessibility tree truncated (limit \(maxElements) elements / depth \(maxDepth)); use observe with root_element_id and snapshot_id to inspect a subtree.")
        }
        return AXSnapshot(text: lines.joined(separator: "\n"), elements: elements, truncated: truncated, nodes: nodes)
    }

    func element(id: Int) -> AXUIElement? {
        elements[id]
    }

    func clear() {
        elements.removeAll(keepingCapacity: true)
        descriptors.removeAll(keepingCapacity: true)
        nextID = 0
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

    private func walk(_ element: AXUIElement, parentID: Int?, index: Int, depth: Int,
                      nodes: inout [AXSnapshotNode], visited: inout [CFHashCode: [AXUIElement]],
                      previous: [CFHashCode: [(id: Int, element: AXUIElement)]]) {
        let hash = CFHash(element)
        guard !(visited[hash] ?? []).contains(where: { CFEqual($0, element) }) else { return }
        guard elements.count < maxElements, depth <= maxDepth else { truncated = true; return }
        visited[hash, default: []].append(element)
        // Hashes are only lookup buckets. AX identity, not a title or tree position,
        // determines whether an ID survives the next observation.
        let id: Int
        if let existing = previous[hash]?.first(where: { CFEqual($0.element, element) }) {
            id = existing.id
        } else {
            id = nextID
            nextID += 1
        }
        elements[id] = element
        let saved = descriptor(element)
        descriptors[id] = saved
        nodes.append(node(element, id: id, parentID: parentID, index: index, depth: depth, descriptor: saved))
        for (childIndex, child) in axElements(element, attribute: kAXChildrenAttribute as String).enumerated() {
            if elements.count >= maxElements { truncated = true; break }
            walk(child, parentID: id, index: childIndex, depth: depth + 1, nodes: &nodes, visited: &visited, previous: previous)
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

    private func node(_ element: AXUIElement, id: Int, parentID: Int?, index: Int, depth: Int,
                      descriptor: AXElementDescriptor) -> AXSnapshotNode {
        var fields: [String: Any] = ["role": descriptor.role]
        var parts = ["[\(id)]", descriptor.role]
        let strings: [(String, String?)] = [
            ("subrole", axString(element, kAXSubroleAttribute as String)),
            ("title", descriptor.title), ("description", descriptor.description),
            ("identifier", descriptor.identifier), ("value", descriptor.value),
            ("selected_text", axString(element, kAXSelectedTextAttribute as String)),
        ]
        for (name, value) in strings {
            guard let value, !value.isEmpty else { continue }
            let clipped = String(value.prefix(300))
            fields[name] = clipped
            if value.count > 300 {
                fields[name + "_truncated"] = true
                fields[name + "_sha256"] = SHA256.hash(data: Data(value.utf8)).map { String(format: "%02x", $0) }.joined()
            }
            let escaped = clipped.replacingOccurrences(of: "\\", with: "\\\\")
                .replacingOccurrences(of: "\"", with: "\\\"").replacingOccurrences(of: "\n", with: "\\n")
                .replacingOccurrences(of: "\r", with: "\\r")
            parts.append("\(name)=\"\(escaped)\(value.count > 300 ? "…" : "")\"")
            if let digest = fields[name + "_sha256"] { parts.append("\(name)_sha256=\(digest)") }
        }
        for (name, attribute) in [("enabled", kAXEnabledAttribute), ("focused", kAXFocusedAttribute),
                                  ("selected", kAXSelectedAttribute), ("expanded", kAXExpandedAttribute)] {
            if let value = axBool(element, attribute as String) {
                fields[name] = value
                parts.append("\(name)=\(value)")
            }
        }
        if let value = axValue(element, kAXSelectedTextRangeAttribute as String), CFGetTypeID(value) == AXValueGetTypeID() {
            var range = CFRange()
            if AXValueGetValue(value as! AXValue, .cfRange, &range), range.location >= 0, range.length >= 0 {
                fields["selection_range"] = ["location": range.location, "length": range.length, "unit": "utf16"]
                parts.append("selection=(\(range.location),\(range.length))")
            }
        }
        if let frame = descriptor.frame {
            fields["frame"] = ["x": frame.minX, "y": frame.minY, "width": frame.width, "height": frame.height]
            parts.append("frame=(\(Int(frame.origin.x)),\(Int(frame.origin.y)),\(Int(frame.width)),\(Int(frame.height)))")
        }
        let actions = axActions(element)
        if !actions.isEmpty {
            fields["actions"] = actions
            parts.append("actions=[\(actions.joined(separator: ","))]")
        }
        return AXSnapshotNode(id: id, parentID: parentID, index: index, attributes: fields,
                              text: String(repeating: "  ", count: depth) + parts.joined(separator: " "))
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
