package appserver

import (
	"errors"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

var errPluginGenerationRefreshBusy = errors.New("plugin generation refresh lease is busy")

const (
	pluginGenerationFallbackPollInterval = 30 * time.Second
	pluginGenerationRetryInterval        = 250 * time.Millisecond
	pluginGenerationCloseCheckInterval   = 250 * time.Millisecond
	pluginGenerationPollingInterval      = time.Second
)

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
				if !errors.Is(err, errPluginGenerationRefreshBusy) {
					providers.DebugLogf("refresh observed plugin generation: %v", err)
				}
				if retry == nil {
					retry = time.NewTimer(pluginGenerationRetryInterval)
				} else {
					if !retry.Stop() {
						select {
						case <-retry.C:
						default:
						}
					}
					retry.Reset(pluginGenerationRetryInterval)
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
		if err := s.refreshPluginGenerationIfChanged(); err != nil {
			providers.DebugLogf("refresh observed plugin generation: %v", err)
		}
	}
}

func (s *Server) refreshPluginGenerationIfChanged() error {
	if s == nil || s.rt == nil || s.rt.WuuHome == "" || s.closed.Load() {
		return nil
	}
	observedEpoch, err := session.ReadPluginGenerationEpoch(s.rt.WuuHome)
	if err != nil {
		return err
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
	defer func() {
		s.pluginGenerationRefreshMu.Unlock()
		if refreshed {
			s.retireIdlePluginRuntimes()
		}
	}()
	observedEpoch, err = session.ReadPluginGenerationEpoch(s.rt.WuuHome)
	if err != nil || (observedEpoch == s.pluginGenerationEpoch.Load() && !s.rt.PluginGenerationNeedsRecovery()) {
		return err
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
		return nil
	}
	catalog, catalogAcquired, err := session.TryAcquirePluginCatalogReadLease(s.rt.WuuHome)
	if err != nil {
		return err
	}
	if !catalogAcquired {
		return errPluginGenerationRefreshBusy
	}
	epoch, err = session.ReadPluginGenerationEpoch(s.rt.WuuHome)
	if err != nil {
		_ = catalog.Release()
		return err
	}
	// Snapshot complete disk state before opening any effectful activation.
	// A plugin's activate callback may itself publish another catalog change.
	// Keep this observed epoch with the prepared candidate. Publication can
	// advance again during activation; recording that newer epoch here would
	// hide a change this candidate never loaded. The next refresh adopts it.

	var candidate *runtime.PluginGeneration
	if s.refreshExtensionsForTest == nil {
		candidate, err = s.rt.PreflightExtensions(s.currentExtensionConfig())
	}
	releaseErr := catalog.Release()
	if err != nil {
		return err
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
		err = s.refreshExtensions(s.currentExtensionConfig())
	}
	if err != nil {
		if !runtime.PluginGenerationWasCommitted(err) {
			return err
		}
		providers.DebugLogf("refresh plugin generation: %v", err)
	}
	s.schedulePluginTurnLifecycleReplay()
	inventory, skills := s.currentExtensionInventory(), s.skillSummaries(s.rt.Skills, s.rt.RootDir)
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
