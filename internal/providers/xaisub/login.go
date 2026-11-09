package xaisub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
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
	closed  bool
}

type pendingLogin struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	credentials credentialstore.Store
	release     func()
	device      DeviceCode
	interval    time.Duration
	deadline    time.Time
}

// NewLoginHub creates an empty SuperGrok login tracker.
func NewLoginHub() *LoginHub {
	return &LoginHub{pending: map[string]*pendingLogin{}}
}

// Start captures the credential source for a device login. On success the hub
// owns release; on failure ownership remains with the caller.
func (h *LoginHub) Start(ctx context.Context, selected credentialstore.Store, release func()) (LoginStart, error) {
	if selected == nil {
		return LoginStart{}, authstorage.ErrUnavailable
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return LoginStart{}, errors.New("xAI login tracker is closed")
	}
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
	if h.closed {
		return LoginStart{}, errors.New("xAI login tracker is closed")
	}
	loginCtx, cancel := context.WithCancel(context.Background())
	h.pending[id] = &pendingLogin{
		ctx: loginCtx, cancel: cancel, credentials: selected, release: release,
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
func (h *LoginHub) Poll(ctx context.Context, loginID, baseURL string) (LoginPollStatus, error) {
	h.mu.Lock()
	pending := h.pending[loginID]
	h.mu.Unlock()
	if pending == nil {
		return LoginPollStatus{Status: LoginFailed, Error: "xAI SuperGrok login is no longer pending"}, nil
	}
	pending.mu.Lock()
	terminal := false
	defer func() {
		if terminal {
			// Queued pollers must observe completion before the lock is released.
			pending.cancel()
		}
		pending.mu.Unlock()
		if terminal {
			h.Cancel(loginID)
		}
	}()
	pollCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(pending.ctx, cancel)
	defer func() { stop(); cancel() }()
	if pending.ctx.Err() != nil {
		return LoginPollStatus{}, errors.New("login cancelled")
	}
	if time.Now().After(pending.deadline) {
		terminal = true
		return LoginPollStatus{Status: LoginExpired, Error: "xAI SuperGrok device code expired; sign in again"}, nil
	}
	tokens, err := ExchangeDeviceCode(pollCtx, nil, pending.device)
	if pending.ctx.Err() != nil {
		return LoginPollStatus{}, errors.New("login cancelled")
	}
	if err == nil {
		terminal = true
		if persistErr := SaveTokens(pollCtx, pending.credentials, tokens, baseURL); persistErr != nil {
			return LoginPollStatus{}, persistErr
		}
		return LoginPollStatus{Status: LoginSuccess, Interval: pending.interval}, nil
	}
	if errors.Is(err, errAuthorizationPending) {
		return LoginPollStatus{Status: LoginPending, Interval: pending.interval + oauthPollingSafetyMargin}, nil
	}
	if errors.Is(err, errSlowDown) {
		pending.interval += slowDownIncrement
		return LoginPollStatus{Status: LoginPending, Interval: pending.interval + oauthPollingSafetyMargin}, nil
	}
	terminal = true
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
	pending := h.pending[loginID]
	delete(h.pending, loginID)
	h.mu.Unlock()
	if pending == nil {
		return
	}
	pending.cancel()
	// In-flight polling must finish before its captured generation is released.
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.release != nil {
		pending.release()
		pending.release = nil
	}
}

// Close cancels pending logins and releases their captured service generations.
func (h *LoginHub) Close() {
	h.mu.Lock()
	h.closed = true
	ids := make([]string, 0, len(h.pending))
	for id := range h.pending {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.Cancel(id)
	}
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
