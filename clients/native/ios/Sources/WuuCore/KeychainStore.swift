import Foundation
import Security

public struct KeychainStore {
    private let service = "ai.wuu.native.account"
    public init() {}
    public func load<T: Decodable>(_ key: String, as type: T.Type) throws -> T? {
        var query = base(key)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw failure(status) }
        return try JSONDecoder().decode(type, from: data)
    }
    public func save<T: Encodable>(_ value: T, key: String) throws {
        let data = try JSONEncoder().encode(value)
        let attributes: [String: Any] = [kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        let status = SecItemUpdate(base(key) as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            let added = SecItemAdd(base(key).merging(attributes) { _, new in new } as CFDictionary, nil)
            guard added == errSecSuccess else { throw failure(added) }
        } else if status != errSecSuccess { throw failure(status) }
    }
    public func delete(_ key: String) throws {
        let status = SecItemDelete(base(key) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw failure(status) }
    }
    private func base(_ key: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
         kSecAttrAccount as String: key]
    }
    private func failure(_ status: OSStatus) -> NativeError {
        .invalid("Secure storage: \(SecCopyErrorMessageString(status, nil) as String? ?? String(status))")
    }
}
