package credentialstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
	keyring "github.com/zalando/go-keyring"
)

var errKeyringNotFound = keyring.ErrNotFound

const keyringEnvelopePrefix = "wuu-credential-v1:"

// The authority record contains no credential data. An uncommitted revision
// fences both a crashed writer's external backend command and a deleted entry.
type keyringRevision struct {
	Revision  string `json:"revision"`
	Committed bool   `json:"committed"`
}

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

func (s *KeyringStore) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	lock, err := s.lock(ctx, namespace, key)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return s.get(namespace, key, lock.Name()+".revision")
}

func (s *KeyringStore) get(namespace, key, revisionPath string) ([]byte, error) {
	revision, err := readKeyringRevision(revisionPath)
	if err != nil {
		return nil, err
	}
	if revision != nil && !revision.Committed {
		return nil, ErrNotFound
	}
	value, err := s.backend.Get(s.service, credentialAccount(namespace, key))
	if errors.Is(err, errKeyringNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("credentialstore keyring get: %w", err)
	}
	if strings.HasPrefix(value, keyringEnvelopePrefix) {
		storedRevision, payload, ok := strings.Cut(strings.TrimPrefix(value, keyringEnvelopePrefix), ":")
		if !ok || revision == nil || storedRevision != revision.Revision {
			return nil, ErrNotFound
		}
		value = payload
	} else if revision != nil {
		// Once authority has been recorded, an old backend command must never
		// make an unversioned credential eligible for legacy migration again.
		return nil, ErrNotFound
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("credentialstore keyring decode: %w", err)
	}
	return data, nil
}

func (s *KeyringStore) set(namespace, key string, value []byte, revisionPath string) error {
	revision, err := newKeyringRevision()
	if err != nil {
		return err
	}
	// Invalidate the previous authority before starting an external Keychain
	// command. That command can survive its parent process and finish late.
	if err := writeKeyringRevision(revisionPath, revision); err != nil {
		return err
	}
	envelope := keyringEnvelopePrefix + revision.Revision + ":" + base64.StdEncoding.EncodeToString(value)
	if err := s.backend.Set(s.service, credentialAccount(namespace, key), envelope); err != nil {
		return fmt.Errorf("credentialstore keyring set: %w", err)
	}
	revision.Committed = true
	return writeKeyringRevision(revisionPath, revision)
}

func (s *KeyringStore) delete(namespace, key, revisionPath string) error {
	revision, err := newKeyringRevision()
	if err != nil {
		return err
	}
	// Keep the revocation record even if the backend is already empty or fails.
	if err := writeKeyringRevision(revisionPath, revision); err != nil {
		return err
	}
	err = s.backend.Delete(s.service, credentialAccount(namespace, key))
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
	return s.set(namespace, key, value, lock.Name()+".revision")
}

func (s *KeyringStore) Delete(ctx context.Context, namespace, key string) error {
	lock, err := s.lock(ctx, namespace, key)
	if err != nil {
		return err
	}
	defer lock.Close()
	return s.delete(namespace, key, lock.Name()+".revision")
}

func (s *KeyringStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	lock, err := s.lock(ctx, namespace, key)
	if err != nil {
		return err
	}
	defer lock.Close()
	revisionPath := lock.Name() + ".revision"
	current, err := s.get(namespace, key, revisionPath)
	if errors.Is(err, ErrNotFound) {
		current = nil
	} else if err != nil {
		return err
	}
	next, err := update(bytes.Clone(current))
	if err != nil {
		return err
	}
	if (current == nil) == (next == nil) && bytes.Equal(current, next) {
		return nil
	}
	if next == nil {
		return s.delete(namespace, key, revisionPath)
	}
	return s.set(namespace, key, next, revisionPath)
}

func newKeyringRevision() (keyringRevision, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return keyringRevision{}, fmt.Errorf("credentialstore keyring revision: %w", err)
	}
	return keyringRevision{Revision: base64.RawURLEncoding.EncodeToString(random[:])}, nil
}

func readKeyringRevision(path string) (*keyringRevision, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credentialstore keyring revision: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("credentialstore keyring revision must be an owner-only regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("credentialstore keyring revision: %w", err)
	}
	var revision keyringRevision
	if err := json.Unmarshal(data, &revision); err != nil {
		return nil, fmt.Errorf("credentialstore keyring revision: %w", err)
	}
	id, err := base64.RawURLEncoding.DecodeString(revision.Revision)
	if err != nil || len(id) != 32 {
		return nil, errors.New("credentialstore keyring revision is invalid")
	}
	return &revision, nil
}

func writeKeyringRevision(path string, revision keyringRevision) error {
	data, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	if err := securefs.WriteFileAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("credentialstore keyring revision write: %w", err)
	}
	return nil
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
