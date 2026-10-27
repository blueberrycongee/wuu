package personalconnectors

import (
	"context"
	"net/url"
	"slices"
	"time"
)

// ensureFresh is called with the broker execution lock held. The secure-store
// lease additionally serializes rotating refresh tokens across host processes.
func (b *Broker) ensureFresh(ctx context.Context, a Actor, id string) error {
	var g Grant
	lease := ""
	err := b.update(ctx, a, func(s *stored) error {
		var ok bool
		g, ok = s.Grants[id]
		if !ok {
			return fault("account_not_connected")
		}
		if g.Connection.State != "connected" || !b.activated(a, g) {
			return fault("authorization_expired")
		}
		if g.NonExpiring || g.ExpiresAt.After(time.Now().Add(60*time.Second)) {
			return nil
		}
		if g.RefreshToken == "" || g.OAuthProvider == "" {
			return fault("authorization_expired")
		}
		if g.RefreshLease != "" {
			return fault("refresh_outcome_unknown")
		}
		lease = nonce()
		g.RefreshLease = lease
		s.Grants[id] = g
		return nil
	})
	if err != nil || lease == "" {
		return err
	}
	c, err := b.clientConfig(ctx, g.OAuthProvider)
	var token OAuthToken
	if err == nil {
		token, err = b.exchangeOAuth(ctx, c, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {g.RefreshToken}})
	}
	if err == nil {
		if len(token.Scopes) == 0 {
			token.Scopes = g.Connection.Scopes
		}
		for _, scope := range g.Connection.Scopes {
			if !slices.Contains(token.Scopes, scope) {
				err = fault("scope_mismatch")
				break
			}
		}
	}
	if err == nil {
		if token.WorkspaceID == "" {
			token.WorkspaceID = g.Connection.WorkspaceID
		}
		token, err = b.verifyOAuth(ctx, g.OAuthProvider, token)
	}
	if err == nil && (token.AccountID != g.Connection.AccountID || g.Connection.WorkspaceID != "" && token.WorkspaceID != g.Connection.WorkspaceID) {
		err = fault("account_identity_mismatch")
	}
	persist := b.update(context.WithoutCancel(ctx), a, func(s *stored) error {
		current, ok := s.Grants[id]
		if !ok || current.Connection.Revision != g.Connection.Revision || current.RefreshLease != lease {
			return fault("connection_changed")
		}
		if err != nil {
			current.Connection.State = "needs_reauth"
			s.Grants[id] = current
			return nil
		}
		current.AccessToken = token.AccessToken
		if token.RefreshToken != "" {
			current.RefreshToken = token.RefreshToken
		}
		current.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		current.RefreshLease = ""
		s.Grants[id] = current
		return nil
	})
	if persist != nil {
		return persist
	}
	return err
}
