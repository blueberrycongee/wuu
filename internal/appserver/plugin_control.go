package appserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/plugincontrol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (s *Server) startPluginControl() {
	if s.rt == nil || s.rt.WuuHome == "" || s.startupErr != nil {
		return
	}
	info := plugincontrol.HostInfo{Workdir: s.rt.RootDir, WorkspaceID: s.rt.WorkspaceID, UserSpaceID: s.rt.UserSpaceID, ProductDomain: s.rt.UserBinding().Domain()}
	registration, err := plugincontrol.Register(s.rt.WuuHome, info, s.handlePluginControl)
	if err != nil {
		providers.DebugLogf("register plugin publication host: %v", err)
		return
	}
	s.pluginControl = registration
}

func (s *Server) pluginControlStatus(id string) plugincontrol.Response {
	snapshot, release := s.rt.RetainExtensionSnapshot()
	defer release()
	result := plugincontrol.Response{GenerationID: snapshot.GenerationID, Plugins: []plugincontrol.PluginStatus{}}
	for _, item := range snapshot.Plugins {
		if id != "" && item.ID != id {
			continue
		}
		status := plugincontrol.PluginStatus{ID: item.ID, SubjectID: item.SubjectID, StorageSubjectID: item.StorageSubject(), Fingerprint: item.Fingerprint, State: "inactive"}
		if snapshot.PluginHost != nil {
			for _, runtime := range snapshot.PluginHost.Statuses() {
				if runtime.ID == item.ID {
					status.State = string(runtime.State)
					status.Error = runtime.Error
					break
				}
			}
		}
		result.Plugins = append(result.Plugins, status)
	}
	return result
}

func (s *Server) handlePluginControl(ctx context.Context, request plugincontrol.Request) plugincontrol.Response {
	var err error
	if s.closed.Load() {
		return plugincontrol.Response{Error: "plugin host is closing"}
	}
	switch request.Operation {
	case "status":
	case "prepare":
		err = s.preparePluginPublication(ctx, request)
	case "observe":
		err = s.observePluginPublication(ctx, request)
	case "reload":
		err = s.reloadPluginControl()
	default:
		err = errors.New("unsupported plugin host operation")
	}
	result := s.pluginControlStatus(request.PluginID)
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

func (s *Server) observePluginPublication(ctx context.Context, request plugincontrol.Request) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := s.refreshPluginGenerationIfChanged()
		pending := errors.Is(err, errPluginGenerationRefreshBusy)
		if err == nil {
			// Additive preparation admits work on the old graph before the
			// candidate is committed. Admission readiness is not activation.
			pending = s.pluginAdditivePreparation.Load() != nil || s.pluginGenerationRefreshing.Load()
			for _, status := range s.pluginControlStatus(request.PluginID).Plugins {
				pending = pending || (status.State == "active" && status.Fingerprint != request.Fingerprint)
			}
		}
		if !pending {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// The publisher owns the catalog writer lease. Serialize with this host's
// refreshes, but do not reacquire the catalog lock or run activation effects.
func (s *Server) preparePluginPublication(ctx context.Context, request plugincontrol.Request) error {
	if !s.pluginGenerationRefreshMu.TryLock() {
		return errors.New("plugin host is refreshing; retry publication")
	}
	defer s.pluginGenerationRefreshMu.Unlock()
	if !s.configRefreshMu.TryLock() {
		return errors.New("plugin host configuration is refreshing; retry publication")
	}
	defer s.configRefreshMu.Unlock()
	authorization, err := pluginpkg.ReadDevAuthorization(s.rt.WuuHome, request.PluginID)
	if err != nil {
		return err
	}
	if authorization.Directory != request.SourceDirectory {
		return errors.New("candidate source differs from development authorization")
	}
	candidate, err := pluginpkg.LoadManifest(request.ManifestPath, "dev")
	if err != nil {
		return err
	}
	if candidate.ID != request.PluginID || candidate.Fingerprint != request.Fingerprint {
		return errors.New("candidate changed before host preparation")
	}
	candidate.AuthorizedDev = true
	candidate.StorageSubjectID = pluginpkg.DevStorageSubject(s.rt.WuuHome, candidate.ID)
	discovered := pluginpkg.Discover(s.rt.RootDir, s.rt.WuuHome)
	replaced := false
	for i, item := range discovered {
		if item.ID == candidate.ID {
			discovered[i] = candidate
			replaced = true
			break
		}
	}
	if !replaced {
		discovered = append(discovered, candidate)
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return err
	}
	if err := s.rt.CheckDiscoveredExtensions(ctx, cfg, discovered, candidate.ID); err != nil {
		return fmt.Errorf("candidate %q rejected: %w", candidate.ID, err)
	}
	return nil
}

func (s *Server) reloadPluginControl() error {
	finish, err := s.beginPluginGenerationMutation("reload", pluginGenerationMutationLive)
	if err != nil {
		return err
	}
	defer finish()
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return err
	}
	err = s.rt.RefreshExtensions(cfg)
	s.pluginRefreshFailure = pluginGenerationRefreshFailure{}
	return errors.Join(err, s.finishPluginGenerationPublication(finish))
}
