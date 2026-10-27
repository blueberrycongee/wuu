package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const (
	configWatchDebounceInterval  = 100 * time.Millisecond
	configWatchRetryInterval     = 500 * time.Millisecond
	configFallbackPollInterval   = 30 * time.Second
	configCloseCheckInterval     = 250 * time.Millisecond
	configWatcherUnavailablePoll = 2 * time.Second
)

func (s *Server) startConfigWatch() {
	if s == nil || s.rt == nil || s.rt.WuuHome == "" || s.startupErr != nil || s.out == nil {
		return
	}
	s.startBackground(func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			s.pollConfigChanges()
			return
		}
		defer watcher.Close()

		paths := s.configWatchPaths()
		watched := make(map[string]struct{})
		addWatchDirectories := func() {
			for _, dir := range configWatchDirectories(paths) {
				if _, exists := watched[dir]; exists {
					continue
				}
				if err := watcher.Add(dir); err != nil {
					providers.DebugLogf("watch config directory %q: %v", dir, err)
					continue
				}
				watched[dir] = struct{}{}
			}
		}
		addWatchDirectories()
		if len(watched) == 0 {
			s.pollConfigChanges()
			return
		}

		fallback := time.NewTicker(configFallbackPollInterval)
		defer fallback.Stop()
		closeCheck := time.NewTicker(configCloseCheckInterval)
		defer closeCheck.Stop()
		var refreshTimer *time.Timer
		var refreshC <-chan time.Time
		defer func() {
			if refreshTimer != nil {
				refreshTimer.Stop()
			}
		}()
		schedule := func(delay time.Duration) {
			if refreshTimer == nil {
				refreshTimer = time.NewTimer(delay)
			} else {
				if !refreshTimer.Stop() {
					select {
					case <-refreshTimer.C:
					default:
					}
				}
				refreshTimer.Reset(delay)
			}
			refreshC = refreshTimer.C
		}
		refresh := func() {
			if err := s.refreshObservedConfigChange(); err != nil {
				providers.DebugLogf("refresh observed config change: %v", err)
				schedule(configWatchRetryInterval)
			}
		}
		// Establish the baseline only after every available source directory is
		// watched so a change cannot slip between the first load and registration.
		refresh()

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					s.pollConfigChanges()
					return
				}
				if event.Op&fsnotify.Create != 0 {
					addWatchDirectories()
				}
				if configWatchEventRelevant(event.Name, paths) {
					schedule(configWatchDebounceInterval)
				}
			case watchErr, ok := <-watcher.Errors:
				if !ok {
					s.pollConfigChanges()
					return
				}
				providers.DebugLogf("watch config: %v", watchErr)
			case <-fallback.C:
				refresh()
			case <-refreshC:
				refreshC = nil
				refresh()
			case <-closeCheck.C:
				if s.closed.Load() {
					return
				}
			}
		}
	})
}

func (s *Server) configWatchPaths() map[string]struct{} {
	if s == nil || s.rt == nil {
		return nil
	}
	candidates := []string{
		s.rt.ConfigPath,
		filepath.Join(s.rt.WuuHome, "config.json"),
		statepath.LegacyConfigPath(s.rt.HomeDir),
		filepath.Join(s.rt.RootDir, ".wuu.json"),
		filepath.Join(s.rt.RootDir, "wuu.json"),
		filepath.Join(s.rt.RootDir, ".wuu", "settings.json"),
		filepath.Join(s.rt.RootDir, ".wuu", "settings.local.json"),
	}
	paths := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		path := strings.TrimSpace(candidate)
		if path == "" {
			continue
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		paths[filepath.Clean(path)] = struct{}{}
	}
	return paths
}

func configWatchDirectories(paths map[string]struct{}) []string {
	seen := make(map[string]struct{}, len(paths))
	dirs := make([]string, 0, len(paths))
	for path := range paths {
		dir := filepath.Dir(path)
		if _, exists := seen[dir]; exists {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	return dirs
}

func configWatchEventRelevant(name string, paths map[string]struct{}) bool {
	path := strings.TrimSpace(name)
	if path == "" {
		return false
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if _, exists := paths[path]; exists {
		return true
	}
	// A settings directory can be created, renamed, or removed as a unit. Its
	// event path is an ancestor of the actual config files, not a config file.
	for candidate := range paths {
		relative, err := filepath.Rel(path, candidate)
		if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *Server) pollConfigChanges() {
	ticker := time.NewTicker(configWatcherUnavailablePoll)
	defer ticker.Stop()
	for !s.closed.Load() {
		<-ticker.C
		if err := s.refreshObservedConfigChange(); err != nil {
			providers.DebugLogf("refresh observed config change: %v", err)
		}
	}
}

func (s *Server) refreshObservedConfigChange() error {
	if s != nil && s.refreshConfigForTest != nil {
		return s.refreshConfigForTest()
	}
	return s.refreshConfigIfChanged()
}

// lockModelConfiguration shares catalog publication's lock order. A watcher
// may inspect fingerprints without blocking turn admission; it fences only
// after discovering a change that requires a new generation.
func (s *Server) lockModelConfiguration() (admit func() error, release func()) {
	if pending := s.pluginAdditivePreparation.Load(); pending != nil && s.configSourceFingerprint() != pending.configSources {
		pending.cancel()
	}
	s.pluginGenerationRefreshMu.Lock()
	s.configRefreshMu.Lock()
	admitted := false
	var once sync.Once
	return func() error {
			if admitted {
				return nil
			}
			if !s.pluginGenerationMutation.CompareAndSwap(false, true) {
				return errors.New("cannot update model configuration while another plugin change is running")
			}
			admitted = true
			return nil
		}, func() {
			once.Do(func() {
				if admitted {
					s.pluginGenerationMutation.Store(false)
				}
				s.configRefreshMu.Unlock()
				s.pluginGenerationRefreshMu.Unlock()
				if admitted {
					s.retireIdlePluginRuntimes()
				}
			})
		}
}

func (s *Server) refreshConfigIfChanged() error {
	if s == nil || s.rt == nil || s.closed.Load() {
		return nil
	}
	admit, unlock := s.lockModelConfiguration()
	defer unlock()
	services, release, err := s.modelServices()
	if err != nil {
		return err
	}
	defer release()
	// Compute under the same lock used to apply an interactive config update.
	// Computing first could leave `next` stale while waiting for the lock, then
	// hot-apply the interactive update a second time after it completed.
	view, err := services.Config.Read(context.Background())
	if err != nil {
		// Poll the selected source again so repair can be observed even without
		// local files. Suppress repeated notifications for the same failure.
		if s.configRejectedSources == err.Error() {
			return nil
		}
		s.configRejectedSources = err.Error()
		if s.out != nil {
			if notifyErr := s.writeNotification(NotificationConfigError, ConfigErrorNotification{Message: err.Error()}); notifyErr != nil {
				return notifyErr
			}
		}
		return err
	}
	cfg := view.Config
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	next := hex.EncodeToString(sum[:])
	recovered := s.configRejectedSources != ""
	if next == s.configFingerprint && !recovered {
		return nil
	}
	// First observation only establishes the baseline. Later changes hot-apply
	// the effective config and then publish a config/changed notification.
	if s.configFingerprint == "" && !recovered {
		s.configFingerprint = next
		return nil
	}
	if err := admit(); err != nil {
		return err
	}
	if err := s.applyExternalConfigChangeLocked(cfg); err != nil {
		// Keep the previous fingerprint so the watcher retries after the user
		// fixes the file instead of permanently swallowing the broken edit.
		return err
	}
	s.configFingerprint = next
	s.configRejectedSources = ""
	if s.out == nil {
		return nil
	}
	return s.writeNotification(NotificationConfigChanged, s.currentConfigChangedNotification())
}

func (s *Server) applyExternalConfigChangeLocked(cfg config.Config) error {
	if s == nil || s.rt == nil {
		return errors.New("runtime session is required")
	}
	providerCfg, resolvedName, err := cfg.ResolveProvider("")
	if err != nil {
		return err
	}
	model := strings.TrimSpace(providerCfg.Model)
	if model == "" {
		return errors.New("default provider has no model")
	}
	variant := strings.TrimSpace(cfg.Agent.Variant)
	effort := strings.TrimSpace(cfg.Agent.Effort)
	ruleProviderName, ruleProviderCfg := modelcatalog.EnrichProvider(resolvedName, providerCfg, model)

	previousProvider := s.rt.ProviderName
	previousRuleProviderCfg := s.rt.ModelRoles.Main.RuleProviderConfig
	if previousRuleProviderCfg.Type == "" {
		previousRuleProviderCfg = s.rt.ModelRoles.Main.ProviderConfig
	}
	providerClientChanged := providerClientConfigChanged(previousRuleProviderCfg, ruleProviderCfg)
	connectionChanged := resolvedName != previousProvider || providerClientChanged

	selection := modelvariant.ResolveForProvider(ruleProviderName, ruleProviderCfg, model, variant, effort)
	roleSelections, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{
		ProviderName:   resolvedName,
		ProviderConfig: providerCfg,
		Model:          model,
		Effort:         selection.LegacyEffort,
		Variant:        selection.Variant,
	})
	if err != nil {
		return err
	}

	return s.applyModelSelectionToRuntime(cfg, resolvedName, model, ruleProviderName, ruleProviderCfg, selection, roleSelections, connectionChanged, true)
}

// Catalog refresh admission keys come from the selected provider too. Local
// file events are only wake-up hints; external sources remain observable.
func (s *Server) configSourceFingerprint() string {
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return "error:" + err.Error()
	}
	defer release()
	view, err := services.Config.Read(context.Background())
	if err != nil {
		return "error:" + err.Error()
	}
	data, err := json.Marshal(view.Config)
	if err != nil {
		return "error:invalid selected configuration"
	}
	sum := sha256.Sum256(data)
	return view.Revision + ":" + hex.EncodeToString(sum[:])
}

func (s *Server) effectiveConfigFingerprint() (string, error) {
	if s == nil || s.rt == nil {
		return "", errors.New("runtime session is required")
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Server) currentConfigChangedNotification() ConfigChangedNotification {
	return ConfigChangedNotification{
		Provider:      s.rt.ProviderName,
		Model:         s.rt.Model,
		Effort:        s.currentDisplayEffort(),
		Variant:       s.currentVariant(),
		ModelRoles:    s.currentModelRoleSummaries(),
		ModelAliases:  s.currentModelAliasSummaries(),
		ProjectModels: s.rt.ProjectModels,
		Providers:     s.providerSummaries(),
	}
}

// recordPublishedConfig observes only the configuration captured by the graph
// just published, never a later source read. Callers hold configRefreshMu.
// A concurrent external edit therefore still differs and wakes the watcher.
func (s *Server) recordPublishedConfig(cfg config.Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	s.configFingerprint = hex.EncodeToString(sum[:])
	s.configRejectedSources = ""
	return nil
}
