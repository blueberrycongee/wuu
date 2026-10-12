package credentialstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

var errKeyringNotFound = keyring.ErrNotFound

type keyringBackend interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

type systemKeyringBackend struct{}

func (systemKeyringBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}
func (systemKeyringBackend) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}
func (systemKeyringBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

type KeyringStore struct {
	service string
	backend keyringBackend
}

func NewKeyringStore(service string, backend keyringBackend) *KeyringStore {
	service = strings.TrimSpace(service)
	if service == "" {
		service = "wuu"
	}
	if backend == nil {
		backend = systemKeyringBackend{}
	}
	return &KeyringStore{service: service, backend: backend}
}

// NewDesktopStore deliberately has no secure-file fallback. A desktop build
// must surface Keychain/keyring failures to the user instead of silently
// writing OAuth credentials to disk.
func NewDesktopStore() (Store, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("desktop credential store requires macOS Keychain on %s", runtime.GOOS)
	}
	return NewKeyringStore("wuu", nil), nil
}

func (s *KeyringStore) Get(_ context.Context, namespace, key string) ([]byte, error) {
	value, err := s.backend.Get(s.service, credentialAccount(namespace, key))
	if errors.Is(err, errKeyringNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("credentialstore keyring get: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("credentialstore keyring decode: %w", err)
	}
	return data, nil
}

func (s *KeyringStore) set(namespace, key string, value []byte) error {
	if err := s.backend.Set(s.service, credentialAccount(namespace, key), base64.StdEncoding.EncodeToString(value)); err != nil {
		return fmt.Errorf("credentialstore keyring set: %w", err)
	}
	return nil
}

func (s *KeyringStore) delete(namespace, key string) error {
	err := s.backend.Delete(s.service, credentialAccount(namespace, key))
	if errors.Is(err, errKeyringNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("credentialstore keyring delete: %w", err)
	}
	return nil
}

func credentialAccount(namespace, key string) string {
	return strings.TrimSpace(namespace) + ":" + strings.TrimSpace(key)
}

func (s *KeyringStore) Set(ctx context.Context, namespace, key string, value []byte) error {
	lock, err := s.lock(ctx, namespace, key)
	if err != nil {
		return err
	}
	defer lock.Close()
	return s.set(namespace, key, value)
}

func (s *KeyringStore) Delete(ctx context.Context, namespace, key string) error {
	lock, err := s.lock(ctx, namespace, key)
	if err != nil {
		return err
	}
	defer lock.Close()
	return s.delete(namespace, key)
}

func (s *KeyringStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	lock, err := s.lock(ctx, namespace, key)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := s.Get(ctx, namespace, key)
	if errors.Is(err, ErrNotFound) {
		current = nil
	} else if err != nil {
		return err
	}
	next, err := update(current)
	if err != nil {
		return err
	}
	if (current == nil) == (next == nil) && bytes.Equal(current, next) {
		return nil
	}
	if next == nil {
		return s.delete(namespace, key)
	}
	return s.set(namespace, key, next)
}

func (s *KeyringStore) lock(ctx context.Context, namespace, key string) (*os.File, error) {
	// Keychain is shared across projects and WUU_HOME overrides. Rendezvous in
	// the OS user's home, rather than a project-specific application directory.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(s.service + "\x00" + credentialAccount(namespace, key)))
	return acquireStoreLock(ctx, filepath.Join(home, ".wuu", "credential-locks", fmt.Sprintf("%x.lock", digest)))
}
