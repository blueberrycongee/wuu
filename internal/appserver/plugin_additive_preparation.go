package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// This is an admission proof for one unpublished, strictly additive catalog,
// not the applied epoch. Existing work keeps the committed graph until publish.
type pluginAdditivePreparation struct {
	epoch         uint64
	base          *runtime.PluginGeneration
	configSources string
	ctx           context.Context
	cancel        context.CancelFunc
}

func (s *Server) cancelPluginAdditivePreparation() {
	if pending := s.pluginAdditivePreparation.Load(); pending != nil {
		pending.cancel()
	}
}

// No provider RPC belongs here: durable admission calls this under th.mu.
// The outer observation checks configuration authority before taking that lock.
func (s *Server) pluginAdditiveAdmissionMatches(epoch uint64) bool {
	pending := s.pluginAdditivePreparation.Load()
	return pending != nil && pending.epoch == epoch && pending.ctx.Err() == nil &&
		!s.closed.Load() && !s.pluginGenerationMutation.Load() && !s.pluginGenerationRefreshing.Load() &&
		s.rt.PluginGenerationAcceptsAdditivePreparation(pending.base)
}

// The caller transfers refreshMu -> configMu ownership to this worker. Process
// callbacks run only after immutable snapshots release the cross-process lease.
func (s *Server) startPluginAdditivePreparation(epoch uint64, base *runtime.PluginGeneration, cfg config.Config, configSources string, discovered []pluginpkg.Plugin, prepared *runtime.PluginCatalogPreparation, published, finish func(), recordFailure func(error) error) bool {
	if strings.HasPrefix(configSources, "error:") {
		return false
	}
	catalogBytes, err := json.Marshal(discovered)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(catalogBytes)
	ctx, cancel := context.WithCancel(context.Background())
	pending := &pluginAdditivePreparation{epoch: epoch, base: base, configSources: configSources, ctx: ctx, cancel: cancel}
	s.pluginAdditivePreparation.Store(pending)
	s.pluginGenerationRefreshing.Store(false)
	started := s.startBackground(func() {
		defer finish()
		defer s.rt.ReleasePluginGeneration(base)
		defer cancel()
		defer s.pluginAdditivePreparation.CompareAndSwap(pending, nil)
		candidate, err := prepared.Build(ctx)
		if err != nil {
			if ctx.Err() == nil {
				recordFailure(err)
				providers.DebugLogf("prepare additive plugin generation: %v", err)
			}
			return
		}
		defer func() {
			if candidate != nil {
				s.rt.ReleasePluginGeneration(candidate)
			}
		}()
		// Close admission before checking the effectful publication boundary.
		s.pluginGenerationRefreshing.Store(true)
		s.pluginAdditivePreparation.CompareAndSwap(pending, nil)
		if ctx.Err() != nil || s.closed.Load() || s.pluginGenerationMutation.Load() || !s.rt.PluginGenerationAcceptsAdditivePreparation(base) {
			return
		}
		lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(s.rt.WuuHome)
		if err != nil || !acquired {
			return
		}
		defer lease.Release()
		catalog, acquired, err := session.TryAcquirePluginCatalogReadLease(s.rt.WuuHome)
		if err != nil || !acquired {
			return
		}
		defer catalog.Release()
		currentEpoch, err := session.ReadPluginGenerationEpoch(s.rt.WuuHome)
		if err != nil || currentEpoch != epoch || s.configSourceFingerprint() != configSources {
			return
		}
		currentBytes, err := json.Marshal(pluginpkg.Discover(s.rt.RootDir, s.rt.WuuHome))
		if err != nil || sha256.Sum256(currentBytes) != digest {
			return
		}
		if err := catalog.Release(); err != nil || ctx.Err() != nil || s.pluginGenerationMutation.Load() {
			return
		}
		refreshed, err := s.publishObservedPluginGeneration(candidate, cfg, epoch, false, lease.Release)
		if refreshed {
			candidate = nil
			published()
		} else if err != nil {
			recordFailure(err)
		}
		if err != nil {
			providers.DebugLogf("publish additive plugin generation: %v", err)
		}
	})
	if !started {
		s.pluginAdditivePreparation.CompareAndSwap(pending, nil)
		cancel()
	}
	return started
}

func (s *Server) publishObservedPluginGeneration(candidate *runtime.PluginGeneration, cfg config.Config, epoch uint64, needsRecovery bool, releaseLease func() error) (bool, error) {
	var err error
	if candidate != nil {
		err = s.rt.ActivatePluginGeneration(candidate, nil)
	} else {
		err = s.refreshExtensions(cfg)
	}
	if err != nil {
		if !runtime.PluginGenerationWasCommitted(err) {
			return false, err
		}
		providers.DebugLogf("refresh plugin generation: %v", err)
	}
	s.pluginRefreshFailure = pluginGenerationRefreshFailure{}
	s.scheduleCurrentPluginTurnLifecycleReplay()
	inventory, skills := s.currentExtensionState()
	if err := releaseLease(); err != nil {
		return true, err
	}
	s.pluginGenerationEpoch.Store(epoch)
	if needsRecovery {
		s.pluginRuntimeRevision.Add(1)
	}
	shell, surfaces := s.rt.ApplicationBindings()
	return true, s.writeNotification(NotificationPluginInventoryChanged, PluginInventoryChangedNotification{
		ApplicationShell: shell, ApplicationSurfaces: surfaces, Epoch: epoch, ExtensionInventory: inventory, Skills: skills,
	})
}
