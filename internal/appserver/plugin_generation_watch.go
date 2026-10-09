package appserver

import (
	"errors"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/blueberrycongee/wuu/internal/providers"
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
	if err != nil || observedEpoch == s.pluginGenerationEpoch.Load() {
		return err
	}

	// Take the same local serialization boundary used by mutations before the
	// cross-process execution lease. Otherwise this watcher can briefly hold a
	// shared lease while a same-server mutation holds the local mutex, causing
	// that mutation's non-blocking exclusive lease attempt to fail spuriously.
	s.pluginGenerationRefreshMu.Lock()
	refreshed := false
	defer func() {
		s.pluginGenerationRefreshMu.Unlock()
		if refreshed {
			s.retireIdlePluginRuntimes()
		}
	}()
	observedEpoch, err = session.ReadPluginGenerationEpoch(s.rt.WuuHome)
	if err != nil || observedEpoch == s.pluginGenerationEpoch.Load() {
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
	if epoch == s.pluginGenerationEpoch.Load() {
		return nil
	}
	inventory, skills, err := s.refreshPluginPackages()
	if err != nil {
		return err
	}
	refreshed = true
	// The refresh mutex still serializes local mutations while the shared lease
	// is dropped and the observed epoch is published.
	if err := lease.Release(); err != nil {
		return err
	}
	s.pluginGenerationEpoch.Store(epoch)
	return s.writeNotification(NotificationPluginInventoryChanged, PluginInventoryChangedNotification{
		Epoch:              epoch,
		ExtensionInventory: inventory,
		Skills:             skills,
	})
}
