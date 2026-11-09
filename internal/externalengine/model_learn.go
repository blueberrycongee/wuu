package externalengine

import (
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

// Discovery publishes only the advertised membership; a model's dependent
// capabilities (efforts, options, speed) become known when a live session
// actually applies that model. Learned entries merge into the discovered
// catalog on read and are keyed by the same discovery context, so a native
// configuration change drops them together with the cached catalog.
var learnedModelCaps = struct {
	sync.Mutex
	byContext map[[32]byte]map[string]DiscoveredModel
}{byContext: map[[32]byte]map[string]DiscoveredModel{}}

// recordSessionModelCapabilities captures the dependent capabilities visible
// for the session's current model after selections were applied.
func recordSessionModelCapabilities(key [32]byte, session acpSession) {
	for _, model := range modelsFromACPSession(session) {
		if !model.IsDefault {
			continue
		}
		if len(model.SupportedEfforts) == 0 && !model.FastMode && len(model.Options) == 0 {
			return
		}
		learnedModelCaps.Lock()
		models := learnedModelCaps.byContext[key]
		if models == nil {
			models = map[string]DiscoveredModel{}
			learnedModelCaps.byContext[key] = models
		}
		models[model.ID] = DiscoveredModel{
			SupportedEfforts: model.SupportedEfforts,
			DefaultEffort:    model.DefaultEffort,
			FastMode:         model.FastMode,
			DefaultSpeed:     model.DefaultSpeed,
			Options:          model.Options,
		}
		learnedModelCaps.Unlock()
		return
	}
}

// discoveryContextKey scopes learned capabilities to the engine's launch and
// native configuration fingerprint, matching the catalog cache key.
func (e *Engine) discoveryContextKey(root string) ([32]byte, bool) {
	key, err := enginecatalog.DiscoveryContext(e.entry.ID, e.binary, root)
	if err != nil {
		return [32]byte{}, false
	}
	return key, true
}

// mergeLearnedModelCaps fills dependent capabilities the advertisement could
// not report with the values live sessions observed for the same model.
func mergeLearnedModelCaps(key [32]byte, models []DiscoveredModel) {
	learnedModelCaps.Lock()
	learned := learnedModelCaps.byContext[key]
	if len(learned) == 0 {
		learnedModelCaps.Unlock()
		return
	}
	out := make(map[string]DiscoveredModel, len(learned))
	for id, model := range learned {
		out[id] = model
	}
	learnedModelCaps.Unlock()
	for i, model := range models {
		capabilities, ok := out[model.ID]
		if !ok {
			continue
		}
		if len(model.SupportedEfforts) == 0 {
			models[i].SupportedEfforts = append([]string(nil), capabilities.SupportedEfforts...)
			models[i].DefaultEffort = strings.TrimSpace(capabilities.DefaultEffort)
		}
		if !model.FastMode && capabilities.FastMode {
			models[i].FastMode = true
			models[i].DefaultSpeed = strings.TrimSpace(capabilities.DefaultSpeed)
		}
		if len(model.Options) == 0 {
			models[i].Options = enginecatalog.CloneModelOptions(capabilities.Options)
		}
	}
}
