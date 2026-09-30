package authstorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const CurrentVersion = 1

var ErrNotFound = errors.New("authstorage: not found")

// SourceSaved marks an API key or auth token the user saved for one provider
// through settings, as opposed to a credential written by an OAuth or
// bootstrap flow. Only a saved credential can outrank a provider type's
// implicit default environment variable.
const SourceSaved = "saved"

type Credentials struct {
	Type         string    `json:"type,omitempty"`
	APIKey       string    `json:"api_key,omitempty"`
	AuthToken    string    `json:"auth_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	AuthMode     string    `json:"auth_mode,omitempty"`
	Source       string    `json:"source,omitempty"`
	BaseURL      string    `json:"base_url,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	LastRefresh  string    `json:"last_refresh,omitempty"`
}

type File struct {
	Version   int                    `json:"version"`
	Providers map[string]Credentials `json:"providers"`
}

type Store struct {
	path string
	mu   *sync.Mutex
}

var pathLocks sync.Map

func New(path string) *Store {
	lock, _ := pathLocks.LoadOrStore(path, &sync.Mutex{})
	return &Store{path: path, mu: lock.(*sync.Mutex)}
}

func ForHome(home string) (*Store, error) {
	path, err := statepath.AuthPath(home)
	if err != nil {
		return nil, err
	}
	return New(path), nil
}

// NewHeadlessCredentialStore is the explicit file-backed credential choice
// for shells that cannot use a system keyring. Desktop callers must use the
// keyring store and surface keyring failures instead of calling this fallback.
func NewHeadlessCredentialStore(home string) (credentialstore.Store, string, error) {
	authPath, err := statepath.AuthPath(home)
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(filepath.Dir(authPath), "credentials.json")
	return credentialstore.NewFileStore(path), path, nil
}

func emptyFile() File {
	return File{Version: CurrentVersion, Providers: map[string]Credentials{}}
}

func (s *Store) Load() (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (File, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{}, ErrNotFound
		}
		return File{}, fmt.Errorf("authstorage: read %s: %w", s.path, err)
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("authstorage: parse %s: %w", s.path, err)
	}
	if file.Version != CurrentVersion {
		return File{}, fmt.Errorf("authstorage: unsupported auth version %d", file.Version)
	}
	if file.Providers == nil {
		file.Providers = map[string]Credentials{}
	}
	return file, nil
}

func (s *Store) writeLocked(file File) error {
	file.Version = CurrentVersion
	if file.Providers == nil {
		file.Providers = map[string]Credentials{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("authstorage: marshal: %w", err)
	}
	if err := securefs.WriteFileAtomic(s.path, append(data, '\n')); err != nil {
		return fmt.Errorf("authstorage: write: %w", err)
	}
	return nil
}

func (s *Store) Get(providerID string) (Credentials, error) {
	file, err := s.Load()
	if err != nil {
		return Credentials{}, err
	}
	credentials, ok := file.Providers[providerID]
	if !ok {
		return Credentials{}, fmt.Errorf("authstorage: no credentials for provider %q", providerID)
	}
	return credentials, nil
}

func (s *Store) Set(providerID string, credentials Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := lockFile(s.path + ".lock")
	if err != nil {
		return err
	}
	defer release()
	file, err := s.loadLocked()
	if errors.Is(err, ErrNotFound) {
		file = emptyFile()
	} else if err != nil {
		return err
	}
	file.Providers[providerID] = credentials
	return s.writeLocked(file)
}

func (s *Store) Update(providerID string, update func(*Credentials)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := lockFile(s.path + ".lock")
	if err != nil {
		return err
	}
	defer release()
	file, err := s.loadLocked()
	if errors.Is(err, ErrNotFound) {
		file = emptyFile()
	} else if err != nil {
		return err
	}
	credentials := file.Providers[providerID]
	update(&credentials)
	file.Providers[providerID] = credentials
	return s.writeLocked(file)
}

func (s *Store) DeleteProvider(providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := lockFile(s.path + ".lock")
	if err != nil {
		return err
	}
	defer release()
	file, err := s.loadLocked()
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	delete(file.Providers, providerID)
	return s.writeLocked(file)
}
