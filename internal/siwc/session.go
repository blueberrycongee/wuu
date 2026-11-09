package siwc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

// AccessToken returns only this registration's token, refreshing near expiry.
// A per-registration OS lock covers read, exchange and atomic replacement so
// separate processes cannot race the same rotating refresh token.
func (m *Manager) AccessToken(ctx context.Context, id string) (string, error) {
	lock, err := m.accountLock(id)
	if err != nil {
		return "", err
	}
	defer lock.Release()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	r, err := m.registration(id)
	if err != nil {
		return "", err
	}
	c, err := m.readCredentials(ctx, r)
	if err != nil {
		return "", err
	}
	if c.PendingIDToken != "" {
		c, err = m.verifyPendingRefresh(ctx, r, c)
		if err != nil {
			return "", err
		}
	}
	if !hasScope(c.Scopes, directScope) {
		return "", ErrPlanDisabled
	}
	now := m.now()
	if now.Add(30 * time.Second).Before(c.ExpiresAt) {
		return c.AccessToken, nil
	}
	if !c.EarliestRefreshAt.IsZero() && now.Before(c.EarliestRefreshAt) {
		if now.Before(c.ExpiresAt) {
			return c.AccessToken, nil
		}
		return "", errors.New("ChatGPT token renewal is not yet available")
	}
	if c.RefreshToken == "" || (!c.RefreshExpiresAt.IsZero() && !now.Before(c.RefreshExpiresAt)) {
		if err = m.clearCredentials(ctx, r); err != nil {
			return "", err
		}
		return "", ErrSignInRequired
	}
	doc, err := m.discover(ctx)
	if err != nil {
		return "", err
	}
	response, err := m.requestTokens(ctx, doc.TokenEndpoint, url.Values{"grant_type": {"refresh_token"}, "client_id": {r.ID}, "refresh_token": {c.RefreshToken}, "resource": {resource}})
	if err != nil {
		if terminalRefreshError(err) {
			if clearErr := m.clearCredentials(context.WithoutCancel(ctx), r); clearErr != nil {
				return "", clearErr
			}
			return "", ErrSignInRequired
		}
		return "", err
	}
	if response.RefreshToken == "" {
		return "", errors.New("ChatGPT refresh response omitted the replacement refresh token")
	}
	next, err := m.makeCredentials(response, r, c.Nonce, c)
	if err != nil {
		return "", err
	}
	// Refresh rotation is irreversible at the issuer. Checkpoint the replacement
	// before JWKS/identity work that can fail transiently. A pending ID token
	// quarantines access until validation succeeds on this or a later request.
	if response.IDToken != "" {
		next.PendingIDToken = response.IDToken
		next.IDToken = c.IDToken
	}
	checkpointCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = m.saveCredentials(checkpointCtx, next); err != nil {
		return "", err
	}
	if next.PendingIDToken != "" {
		next, err = m.verifyPendingRefresh(ctx, r, next)
		if err != nil {
			return "", err
		}
	}
	if !hasScope(next.Scopes, directScope) {
		return "", ErrPlanDisabled
	}
	return next.AccessToken, nil
}
func terminalRefreshError(err error) bool {
	var oauth *OAuthError
	if !errors.As(err, &oauth) {
		return false
	}
	switch oauth.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused":
		return true
	}
	return false
}

// SignOut attempts remote revocation with bounded retries and always clears the
// local session, retaining the issued registration and host for later sign-in.
// A false RevocationConfirmed requires the UI to explain that the user can
// disconnect the app in ChatGPT Settings. Local storage failures are errors.
func (m *Manager) SignOut(ctx context.Context, id string) (SignOutResult, error) {
	lock, err := m.accountLock(id)
	if err != nil {
		return SignOutResult{}, err
	}
	defer lock.Release()
	r, err := m.registration(id)
	if err != nil {
		return SignOutResult{}, err
	}
	c, readErr := m.readCredentials(ctx, r)
	// Fence all processes before waiting for the network. Retain the refresh
	// token in this protected operation until revocation has been attempted.
	if err = m.fenceCredentials(r); err != nil {
		return SignOutResult{}, err
	}
	result := SignOutResult{}
	if readErr == nil && c.RefreshToken != "" {
		doc, discoveryErr := m.discover(ctx)
		if discoveryErr == nil {
			for attempt := 0; attempt < 3; attempt++ {
				if attempt > 0 {
					timer := time.NewTimer(time.Duration(attempt) * 100 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
					case <-timer.C:
					}
					if ctx.Err() != nil {
						break
					}
				}
				resp, requestErr := m.postForm(ctx, doc.RevocationEndpoint, url.Values{"token": {c.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {r.ID}})
				if requestErr != nil {
					if ctx.Err() != nil {
						break
					}
					continue
				}
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					result.RevocationConfirmed = true
					break
				}
				if resp.StatusCode < 500 {
					break
				}
			}
		}
	} else if readErr == nil {
		result.RevocationConfirmed = true
	}
	// A cancelled request must still fence local access after sign-out was asked.
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = m.config.Credentials.Delete(clearCtx, m.namespace, r.ID); err != nil {
		return result, err
	}
	if readErr != nil && !errors.Is(readErr, ErrSignInRequired) {
		return result, readErr
	}
	return result, nil
}

func (m *Manager) verifyPendingRefresh(ctx context.Context, r registration, c credentials) (credentials, error) {
	claims, err := m.verifyIdentity(ctx, c.PendingIDToken, r.ID, c.Nonce, false)
	if err != nil {
		return c, err
	}
	if claims.Subject != r.Subject {
		return c, errIdentityMismatch
	}
	c.IDToken = c.PendingIDToken
	c.PendingIDToken = ""
	if err = m.saveCredentials(ctx, c); err != nil {
		return c, err
	}
	return c, nil
}
