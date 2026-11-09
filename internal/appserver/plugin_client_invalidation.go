package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func (s *Server) invalidatePluginClient(ctx context.Context, owner *pluginhost.Host, event pluginhost.ClientInvalidation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.rt == nil || s.closed.Load() {
		return errServerClosed
	}
	if !s.rt.UserBinding().Matches(userspace.Binding{UserSpaceID: event.UserSpaceID, ProductDomain: event.ProductDomain}) {
		return errors.New("client invalidation belongs to another user space or product domain")
	}
	_, current, err := s.requireActiveDesktopPluginHost(event.PluginID, event.Fingerprint)
	if err != nil {
		return err
	}
	// A retained turn may still use an older host with the same fingerprint.
	// It must not publish client hints after the active generation changed.
	if owner == nil || owner != current {
		return errors.New("client invalidation generation is no longer current")
	}
	return s.writeNotification("plugin/client/invalidated", event)
}
