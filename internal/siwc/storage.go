package siwc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type registration struct {
	ID       string `json:"client_id"`
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject,omitempty"`
	Email    string `json:"email,omitempty"`
	Revision string `json:"revision"`
}
type diskState struct {
	Version  int                     `json:"version"`
	HostID   string                  `json:"ext_agent_host_id"`
	Accounts map[string]registration `json:"accounts"`
}
type credentials struct {
	ClientID          string    `json:"client_id"`
	Issuer            string    `json:"issuer"`
	Subject           string    `json:"subject"`
	Revision          string    `json:"revision"`
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token,omitempty"`
	IDToken           string    `json:"id_token,omitempty"`
	PendingIDToken    string    `json:"pending_id_token,omitempty"`
	Nonce             string    `json:"nonce,omitempty"`
	Scopes            []string  `json:"scopes"`
	ExpiresAt         time.Time `json:"expires_at"`
	RefreshExpiresAt  time.Time `json:"refresh_expires_at,omitempty"`
	EarliestRefreshAt time.Time `json:"earliest_refresh_at,omitempty"`
}

func (m *Manager) loadState() (diskState, error) {
	state := diskState{Version: 1, Accounts: map[string]registration{}}
	if info, err := os.Lstat(m.config.StateDir); err == nil {
		if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return state, errors.New("ChatGPT state directory must be owner-only")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return state, err
	}
	path := filepath.Join(m.config.StateDir, "state.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return state, errors.New("ChatGPT metadata must be an owner-only regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return state, errors.New("ChatGPT metadata is corrupt")
	}
	if state.Version != 1 || state.Accounts == nil {
		return state, errors.New("unsupported ChatGPT metadata version")
	}
	for id, r := range state.Accounts {
		if id == "" || id == dynamicClient || r.ID != id || r.Issuer != m.config.Issuer || r.Revision == "" {
			return state, errors.New("ChatGPT registration metadata is invalid")
		}
	}
	return state, nil
}
func (m *Manager) mutateState(update func(*diskState) error) error {
	if err := securefs.Mkdir(m.config.StateDir); err != nil {
		return err
	}
	info, err := os.Lstat(m.config.StateDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return errors.New("ChatGPT state directory must be owner-only")
	}
	lock, err := storelock.Acquire(m.config.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	state, err := m.loadState()
	if err != nil {
		return err
	}
	if err = update(&state); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return securefs.WriteFileAtomic(filepath.Join(m.config.StateDir, "state.json"), append(data, '\n'))
}
func (m *Manager) hostID() (string, error) {
	var host string
	err := m.mutateState(func(s *diskState) error {
		if s.HostID == "" {
			var b [16]byte
			if _, err := rand.Read(b[:]); err != nil {
				return err
			}
			b[6] = (b[6] & 0x0f) | 0x40
			b[8] = (b[8] & 0x3f) | 0x80
			s.HostID = fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
		}
		host = s.HostID
		return nil
	})
	return host, err
}
func (m *Manager) accountLock(id string) (*storelock.Lock, error) {
	sum := sha256.Sum256([]byte(id))
	return storelock.Acquire(filepath.Join(m.config.StateDir, "sessions", fmt.Sprintf("%x", sum[:])))
}
func (m *Manager) registration(id string) (registration, error) {
	s, err := m.loadState()
	if err != nil {
		return registration{}, err
	}
	r, ok := s.Accounts[id]
	if !ok {
		return registration{}, ErrNotFound
	}
	return r, nil
}
func (m *Manager) readCredentials(ctx context.Context, r registration) (credentials, error) {
	raw, err := m.config.Credentials.Get(ctx, m.namespace, r.ID)
	if errors.Is(err, credentialstore.ErrNotFound) {
		return credentials{}, ErrSignInRequired
	}
	if err != nil {
		return credentials{}, err
	}
	var c credentials
	if json.Unmarshal(raw, &c) != nil {
		return c, errors.New("ChatGPT protected credentials are corrupt")
	}
	if c.Revision != r.Revision {
		return credentials{}, ErrSignInRequired
	}
	if c.ClientID != r.ID || c.Subject != r.Subject || c.Issuer != r.Issuer || c.Subject == "" {
		return credentials{}, errors.New("ChatGPT credential identity does not match the registration")
	}
	if c.AccessToken == "" {
		return credentials{}, ErrSignInRequired
	}
	return c, nil
}
func (m *Manager) saveCredentials(ctx context.Context, c credentials) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return m.config.Credentials.Set(ctx, m.namespace, c.ClientID, raw)
}
func (m *Manager) fenceCredentials(r registration) error {
	// Fence old tokens before deleting the backend entry. If a keyring delete
	// fails or a crashed writer finishes late, the old set remains unusable.
	revision, err := randomValue()
	if err != nil {
		return err
	}
	if err = m.mutateState(func(s *diskState) error {
		current, ok := s.Accounts[r.ID]
		if !ok {
			return ErrNotFound
		}
		if current.Revision != r.Revision {
			return ErrSignInRequired
		}
		current.Revision = revision
		s.Accounts[r.ID] = current
		return nil
	}); err != nil {
		return err
	}
	cancelAccountRequests(m.namespace, r.ID)
	return nil
}
func (m *Manager) clearCredentials(ctx context.Context, r registration) error {
	if err := m.fenceCredentials(r); err != nil {
		return err
	}
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return m.config.Credentials.Delete(clearCtx, m.namespace, r.ID)
}

func (m *Manager) Account(ctx context.Context, id string) (Account, error) {
	r, err := m.registration(id)
	if err != nil {
		return Account{}, err
	}
	account := Account{ID: r.ID, Email: r.Email, Subject: r.Subject, Issuer: r.Issuer}
	c, err := m.readCredentials(ctx, r)
	if errors.Is(err, ErrSignInRequired) {
		return account, nil
	}
	if err != nil {
		return Account{}, err
	}
	account.Connected = true
	account.PlanEnabled = hasScope(c.Scopes, directScope)
	account.ExpiresAt = c.ExpiresAt
	return account, nil
}
func (m *Manager) ListAccounts(ctx context.Context) ([]Account, error) {
	s, err := m.loadState()
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(s.Accounts))
	for id := range s.Accounts {
		a, err := m.Account(ctx, id)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	return accounts, nil
}
func hasScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}
