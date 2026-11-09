import UIKit
import Capacitor

class SceneDelegate: UIResponder, UIWindowSceneDelegate {
    var window: UIWindow?

    func scene(_ scene: UIScene, willConnectTo session: UISceneSession, options connectionOptions: UIScene.ConnectionOptions) {
        guard let windowScene = scene as? UIWindowScene else { return }

        window = UIWindow(windowScene: windowScene)
        // Native keyboard resizing exposes the window behind its rounded corners.
        window?.backgroundColor = .systemBackground
        window?.rootViewController = WuuViewController()
        window?.makeKeyAndVisible()

        SceneDelegateProxy.shared.scene(scene, willConnectTo: session, options: connectionOptions)
    }

    func scene(_ scene: UIScene, openURLContexts URLContexts: Set<UIOpenURLContext>) {
        SceneDelegateProxy.shared.scene(scene, openURLContexts: URLContexts)
    }

    func scene(_ scene: UIScene, continue userActivity: NSUserActivity) {
        SceneDelegateProxy.shared.scene(scene, continue: userActivity)
    }
}

class WuuViewController: CAPBridgeViewController {
    override func capacitorDidLoad() {
        bridge?.registerPluginInstance(WuuSecureStoragePlugin())
    }
}

import Security

@objc(WuuSecureStoragePlugin)
public class WuuSecureStoragePlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "WuuSecureStoragePlugin"
    public let jsName = "WuuSecureStorage"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "get", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "set", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "remove", returnType: CAPPluginReturnPromise)
    ]
    private func query(_ call: CAPPluginCall) -> [String: Any]? {
        guard let key = call.getString("key"), key.hasPrefix("wuu."), key.count <= 128 else {
            call.reject("Invalid credential key"); return nil
        }
        return [kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: "com.blueberrycongee.wuu.credentials",
                kSecAttrAccount as String: key]
    }
    @objc func get(_ call: CAPPluginCall) {
        guard var q = query(call) else { return }
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &result)
        if status == errSecItemNotFound { call.resolve(); return }
        guard status == errSecSuccess, let data = result as? Data,
              let value = String(data: data, encoding: .utf8) else { call.reject("Credential storage unavailable"); return }
        call.resolve(["value": value])
    }
    @objc func set(_ call: CAPPluginCall) {
        guard var q = query(call), let value = call.getString("value"), let data = value.data(using: .utf8) else { call.reject("Invalid credential"); return }
        let attributes: [String: Any] = [kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        var status = SecItemUpdate(q as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            q.merge(attributes) { _, new in new }
            status = SecItemAdd(q as CFDictionary, nil)
        }
        if status == errSecSuccess { call.resolve() } else { call.reject("Could not save credential") }
    }
    @objc func remove(_ call: CAPPluginCall) {
        guard let q = query(call) else { return }
        let status = SecItemDelete(q as CFDictionary)
        if status == errSecSuccess || status == errSecItemNotFound { call.resolve() } else { call.reject("Could not remove credential") }
    }
}
