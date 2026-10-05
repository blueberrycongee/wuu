package xaisub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// LoginStart is the public device-code challenge shown to the user.
type LoginStart struct {
	LoginID                 string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               time.Duration
	Interval                time.Duration
}

// LoginPollStatus is one non-blocking poll of an outstanding SuperGrok login.
type LoginPollStatus struct {
	Status   string
	Interval time.Duration
	Error    string
}

const (
	LoginPending = "pending"
	LoginSuccess = "success"
	LoginDenied  = "denied"
	LoginExpired = "expired"
	LoginFailed  = "failed"
)

// LoginHub tracks outstanding device-code logins for the app-server UI.
type LoginHub struct {
	mu      sync.Mutex
	pending map[string]*pendingLogin
}

type pendingLogin struct {
	device   DeviceCode
	interval time.Duration
	deadline time.Time
}

// NewLoginHub creates an empty SuperGrok login tracker.
func NewLoginHub() *LoginHub {
	return &LoginHub{pending: map[string]*pendingLogin{}}
}

// Start requests a device code and remembers it under a login id.
func (h *LoginHub) Start(ctx context.Context) (LoginStart, error) {
	device, err := RequestDeviceCode(ctx, nil)
	if err != nil {
		return LoginStart{}, err
	}
	id, err := newLoginID()
	if err != nil {
		return LoginStart{}, err
	}
	interval := device.Interval
	if interval < minPollInterval {
		interval = defaultPollInterval
	}
	expiresIn := device.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = defaultDeviceCodeLifetime
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil {
		h.pending = map[string]*pendingLogin{}
	}
	h.pending[id] = &pendingLogin{
		device:   device,
		interval: interval,
		deadline: time.Now().Add(expiresIn),
	}
	return LoginStart{
		LoginID:                 id,
		UserCode:                device.UserCode,
		VerificationURI:         device.VerificationURI,
		VerificationURIComplete: device.VerificationURIComplete,
		ExpiresIn:               expiresIn,
		Interval:                interval,
	}, nil
}

// Poll performs one token-endpoint check for a previously started login.
func (h *LoginHub) Poll(ctx context.Context, loginID, home, baseURL string) (LoginPollStatus, error) {
	h.mu.Lock()
	pending := h.pending[loginID]
	h.mu.Unlock()
	if pending == nil {
		return LoginPollStatus{Status: LoginFailed, Error: "xAI SuperGrok login is no longer pending"}, nil
	}
	if time.Now().After(pending.deadline) {
		h.Cancel(loginID)
		return LoginPollStatus{Status: LoginExpired, Error: "xAI SuperGrok device code expired; sign in again"}, nil
	}
	tokens, err := ExchangeDeviceCode(ctx, nil, pending.device)
	if err == nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		// Cancellation must win over a token response that arrives afterward.
		if h.pending[loginID] != pending {
			return LoginPollStatus{Status: LoginFailed, Error: "xAI SuperGrok login is no longer pending"}, nil
		}
		if persistErr := PersistTokens(home, tokens, baseURL); persistErr != nil {
			return LoginPollStatus{}, persistErr
		}
		delete(h.pending, loginID)
		return LoginPollStatus{Status: LoginSuccess, Interval: pending.interval}, nil
	}
	if errors.Is(err, errAuthorizationPending) {
		return LoginPollStatus{Status: LoginPending, Interval: pending.interval + oauthPollingSafetyMargin}, nil
	}
	if errors.Is(err, errSlowDown) {
		h.mu.Lock()
		if current := h.pending[loginID]; current != nil {
			current.interval += slowDownIncrement
			pending = current
		}
		h.mu.Unlock()
		return LoginPollStatus{Status: LoginPending, Interval: pending.interval + oauthPollingSafetyMargin}, nil
	}
	h.Cancel(loginID)
	status := LoginFailed
	if err.Error() == "xAI SuperGrok authorization was denied" {
		status = LoginDenied
	}
	if err.Error() == "xAI SuperGrok device code expired; sign in again" {
		status = LoginExpired
	}
	return LoginPollStatus{Status: status, Error: err.Error()}, nil
}

// Cancel drops an outstanding device-code login.
func (h *LoginHub) Cancel(loginID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pending, loginID)
}

// WaitForDevice blocks until the user completes SuperGrok authorization.
func WaitForDevice(ctx context.Context, device DeviceCode) (TokenResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := device.Interval
	if interval < minPollInterval {
		interval = defaultPollInterval
	}
	deadline := time.Now().Add(device.ExpiresIn)
	if device.ExpiresIn <= 0 {
		deadline = time.Now().Add(defaultDeviceCodeLifetime)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	// RFC 8628: wait before the first poll when the authorization server
	// already supplied an interval.
	select {
	case <-ctx.Done():
		return TokenResponse{}, errors.New("login cancelled")
	case <-timer.C:
	}
	for {
		if time.Now().After(deadline) {
			return TokenResponse{}, errors.New("xAI SuperGrok device code expired; sign in again")
		}
		tokens, err := ExchangeDeviceCode(ctx, nil, device)
		if err == nil {
			return tokens, nil
		}
		if errors.Is(err, errSlowDown) {
			interval += slowDownIncrement
		} else if !errors.Is(err, errAuthorizationPending) {
			return TokenResponse{}, err
		}
		wait := interval + oauthPollingSafetyMargin
		remaining := time.Until(deadline)
		if wait > remaining {
			wait = remaining
		}
		if wait <= 0 {
			return TokenResponse{}, errors.New("xAI SuperGrok device code expired; sign in again")
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return TokenResponse{}, errors.New("login cancelled")
		case <-timer.C:
		}
	}
}

func newLoginID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
