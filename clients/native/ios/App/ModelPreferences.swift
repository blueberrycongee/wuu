import Foundation
import CryptoKit
import WuuCore

/// Model choices are local defaults for new conversations, scoped to a connection identity.
struct ModelPreferences {
    private let key: String
    init(scope: [String]) {
        let identity = scope.map { "\($0.utf8.count):\($0)" }.joined()
        key = "wuu.model-preferences.v1." + Data(SHA256.hash(data: Data(identity.utf8))).base64URL
    }
    var hasSavedChoices: Bool { UserDefaults.standard.dictionary(forKey: key)?.isEmpty == false }
    func save(_ settings: ThreadSettings, engine: String) {
        var choices = UserDefaults.standard.dictionary(forKey: key) ?? [:]
        choices[engine] = ["provider": settings.provider, "model": settings.model, "variant": settings.variant]
        UserDefaults.standard.set(choices, forKey: key)
    }
    func selection(engine: String, defaults: ThreadSettings, providers: [RemoteProvider], engines: [RemoteEngine]) -> ThreadSettings? {
        guard let saved = UserDefaults.standard.dictionary(forKey: key)?[engine] as? [String: String],
              let provider = saved["provider"], let model = saved["model"], let variant = saved["variant"],
              let engineInfo = engines.first(where: { $0.id == engine && $0.available }) else { return nil }
        let models: [RemoteModel]
        if engine == "wuu" {
            guard let configured = providers.first(where: { $0.id == provider }) else { return nil }
            models = configured.models
            guard models.contains(where: { $0.id == model }) else { return nil }
        } else {
            models = engineInfo.models
            // Some external engines accept custom models without publishing a catalog.
            guard model.isEmpty || models.isEmpty || models.contains(where: { $0.id == model }) else { return nil }
        }
        var settings = defaults
        settings.provider = provider; settings.model = model
        if let choice = models.first(where: { $0.id == model }) {
            settings.variant = choice.variants.contains(variant) ? variant : ""
        } else {
            settings.variant = variant
        }
        return settings
    }
}
