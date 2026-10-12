package appserver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

var errPluginGenerationRefreshBusy = errors.New("plugin generation refresh lease is busy")
var errPluginGenerationRefreshDeferred = errors.New("plugin generation refresh is waiting to retry")

const (
	pluginGenerationFallbackPollInterval = 30 * time.Second
	pluginGenerationRetryInterval        = 250 * time.Millisecond
	pluginGenerationCloseCheckInterval   = 250 * time.Millisecond
	pluginGenerationPollingInterval      = time.Second
	pluginGenerationFailureRetryInterval = time.Second
	pluginGenerationFailureRetryMax      = 30 * time.Second
)

// Protected by pluginGenerationRefreshMu, shared by the watcher and turn
// admission. Only failed candidates advance this delay, never lease contention.
type pluginGenerationRefreshFailure struct {
	fingerprint [sha256.Size]byte
	inputs      [sha256.Size]byte
	observeAt   time.Time
	delay       time.Duration
	retryAt     time.Time
	err         error
}

func pluginGenerationRefreshPending(err error) bool {
	return errors.Is(err, errPluginGenerationRefreshBusy) || errors.Is(err, errPluginGenerationRefreshDeferred)
}

func (s *Server) startPluginGenerationWatch() {
	if s == nil || s.rt == nil || s.rt.WuuHome == "" || s.startupErr != nil {
		return
	}
	s.startBackground(func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			s.pollPluginGeneration()
			return
		}
		defer watcher.Close()
		if err := watcher.Add(s.rt.WuuHome); err != nil {
			providers.DebugLogf("watch plugin generation: %v", err)
			s.pollPluginGeneration()
			return
		}

		fallback := time.NewTicker(pluginGenerationFallbackPollInterval)
		defer fallback.Stop()
		closeCheck := time.NewTicker(pluginGenerationCloseCheckInterval)
		defer closeCheck.Stop()
		var retry *time.Timer
		var retryC <-chan time.Time
		defer func() {
			if retry != nil {
				retry.Stop()
			}
		}()

		refresh := func() {
			if err := s.refreshPluginGenerationIfChanged(); err != nil {
				if !pluginGenerationRefreshPending(err) {
					providers.DebugLogf("refresh observed plugin generation: %v", err)
				}
				// Continue observing candidate inputs during backoff, including
				// nested file repairs that the home-directory watch cannot see.
				delay := pluginGenerationPollingInterval
				if errors.Is(err, errPluginGenerationRefreshBusy) {
					delay = pluginGenerationRetryInterval
				}
				if retry == nil {
					retry = time.NewTimer(delay)
				} else {
					if !retry.Stop() {
						select {
						case <-retry.C:
						default:
						}
					}
					retry.Reset(delay)
				}
				retryC = retry.C
				return
			}
			if retry != nil {
				retry.Stop()
			}
			retryC = nil
		}
		// Close the setup race: a mutation may land after the startup epoch was
		// read but before the directory watch became active.
		refresh()

		for {
			select {
			case _, ok := <-watcher.Events:
				if !ok {
					s.pollPluginGeneration()
					return
				}
				refresh()
			case watchErr, ok := <-watcher.Errors:
				if !ok {
					s.pollPluginGeneration()
					return
				}
				providers.DebugLogf("watch plugin generation: %v", watchErr)
			case <-fallback.C:
				refresh()
			case <-retryC:
				retryC = nil
				refresh()
			case <-closeCheck.C:
				if s.closed.Load() {
					return
				}
			}
		}
	})
}

func (s *Server) pollPluginGeneration() {
	ticker := time.NewTicker(pluginGenerationPollingInterval)
	defer ticker.Stop()
	for !s.closed.Load() {
		<-ticker.C
		if err := s.refreshPluginGenerationIfChanged(); err != nil && !pluginGenerationRefreshPending(err) {
			providers.DebugLogf("refresh observed plugin generation: %v", err)
		}
	}
}

func (s *Server) refreshPluginGenerationIfChanged() error {
	if s == nil || s.rt == nil || s.rt.WuuHome == "" || s.closed.Load() {
		return nil
	}
	// Take the same local serialization boundary used by mutations before the
	// cross-process execution lease. Otherwise this watcher can briefly hold a
	// shared lease while a same-server mutation holds the local mutex, causing
	// that mutation's non-blocking exclusive lease attempt to fail spuriously.
	// Activation can synchronously submit input through host services. Such
	// admission must queue rather than wait for the activation that called it.
	if !s.pluginGenerationRefreshMu.TryLock() {
		return errPluginGenerationRefreshBusy
	}
	refreshed := false
	invalidated := false
	defer func() {
		if invalidated && !refreshed {
			inventory, skills := s.currentExtensionState()
			if err := s.writeNotification(NotificationPluginInventoryChanged, PluginInventoryChangedNotification{
				Epoch: s.pluginGenerationEpoch.Load(), ExtensionInventory: inventory, Skills: skills,
			}); err != nil {
				providers.DebugLogf("notify invalidated plugin dependencies: %v", err)
			}
		}
		s.pluginGenerationRefreshMu.Unlock()
		if refreshed {
			s.retireIdlePluginRuntimes()
		}
	}()
	// Revoke failed dependencies before an epoch/recovery early return. A pinned
	// older generation may fail while the current generation remains healthy.
	invalidated = s.rt.RevokeFailedPluginDependencies()
	observedEpoch, err := session.ReadPluginGenerationEpoch(s.rt.WuuHome)
	if err != nil {
		return err
	}
	if observedEpoch == s.pluginGenerationEpoch.Load() && !s.rt.PluginGenerationNeedsRecovery() {
		s.pluginRefreshFailure = pluginGenerationRefreshFailure{}
		return nil
	}
	lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(s.rt.WuuHome)
	if err != nil {
		return err
	}
	if !acquired {
		return errPluginGenerationRefreshBusy
	}
	defer lease.Release()
	epoch := lease.Epoch()
	needsRecovery := s.rt.PluginGenerationNeedsRecovery()
	if epoch == s.pluginGenerationEpoch.Load() && !needsRecovery {
		s.pluginRefreshFailure = pluginGenerationRefreshFailure{}
		return nil
	}
	catalog, catalogAcquired, err := session.TryAcquirePluginCatalogReadLease(s.rt.WuuHome)
	if err != nil {
		return err
	}
	if !catalogAcquired {
		return errPluginGenerationRefreshBusy
	}
	defer catalog.Release()
	epoch, err = session.ReadPluginGenerationEpoch(s.rt.WuuHome)
	if err != nil {
		return err
	}
	// Snapshot complete disk state before opening any effectful activation.
	// A plugin's activate callback may itself publish another catalog change.
	// Keep this observed epoch with the prepared candidate. Publication can
	// advance again during activation; recording that newer epoch here would
	// hide a change this candidate never loaded. The next refresh adopts it.
	cfg := s.currentExtensionConfig()
	inputs, err := json.Marshal(struct {
		Epoch         uint64
		ConfigSources string
		Config        config.Config
	}{epoch, s.configSourceFingerprint(), cfg})
	if err != nil {
		return err
	}
	inputFingerprint := sha256.Sum256(inputs)
	failure := &s.pluginRefreshFailure
	// Discovery hashes every package file. Share an observation limit with
	// turn admission so unrelated notifications cannot keep re-reading large
	// packages during backoff. Epoch/config changes and a due retry still get
	// an immediate observation; nested package repairs are polled each second.
	if failure.inputs == inputFingerprint && time.Now().Before(failure.observeAt) && time.Now().Before(failure.retryAt) {
		return errors.Join(errPluginGenerationRefreshDeferred, failure.err)
	}
	discovered := pluginpkg.Discover(s.rt.RootDir, s.rt.WuuHome)
	packages, err := json.Marshal(struct {
		Inputs  [sha256.Size]byte
		Plugins []pluginpkg.Plugin
	}{inputFingerprint, discovered})
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256(packages)
	if failure.fingerprint != fingerprint {
		*failure = pluginGenerationRefreshFailure{fingerprint: fingerprint}
	}
	failure.inputs = inputFingerprint
	failure.observeAt = time.Now().Add(pluginGenerationPollingInterval)
	if time.Now().Before(failure.retryAt) {
		return errors.Join(errPluginGenerationRefreshDeferred, failure.err)
	}
	recordFailure := func(err error) error {
		if failure.delay == 0 {
			failure.delay = pluginGenerationFailureRetryInterval
		} else {
			failure.delay = min(2*failure.delay, pluginGenerationFailureRetryMax)
		}
		failure.retryAt = time.Now().Add(failure.delay)
		failure.err = err
		return err
	}

	var candidate *runtime.PluginGeneration
	if s.refreshExtensionsForTest == nil {
		candidate, err = s.rt.PreflightDiscoveredExtensions(cfg, discovered)
	}
	releaseErr := catalog.Release()
	if err != nil {
		return recordFailure(err)
	}
	if releaseErr != nil {
		if candidate != nil {
			s.rt.ReleasePluginGeneration(candidate)
		}
		return releaseErr
	}
	if candidate != nil {
		err = s.rt.ActivatePluginGeneration(candidate, nil)
	} else {
		err = s.refreshExtensions(cfg)
	}
	if err != nil {
		if !runtime.PluginGenerationWasCommitted(err) {
			return recordFailure(err)
		}
		providers.DebugLogf("refresh plugin generation: %v", err)
	}
	*failure = pluginGenerationRefreshFailure{}
	s.schedulePluginTurnLifecycleReplay()
	inventory, skills := s.currentExtensionState()
	refreshed = true
	// The refresh mutex still serializes local mutations while the shared lease
	// is dropped and the observed epoch is published.
	if err := lease.Release(); err != nil {
		return err
	}
	s.pluginGenerationEpoch.Store(epoch)
	if needsRecovery {
		s.pluginRuntimeRevision.Add(1)
	}
	return s.writeNotification(NotificationPluginInventoryChanged, PluginInventoryChangedNotification{
		Epoch:              epoch,
		ExtensionInventory: inventory,
		Skills:             skills,
	})
}
