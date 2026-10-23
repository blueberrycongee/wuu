package authstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/grokbuildspec"
)

type nativeStore struct {
	credentialstore.Store
	codexPath, grokPath string
	legacyPath          string
	migrationMu         sync.Mutex
	migrated            bool
}

// NewNativeStore is called only when the explicitly selected native credential
// provider activates. Migration is atomic and one-time; external providers are
// never wrapped or made to consult a local recovery file.
func NewNativeStore(base credentialstore.Store, resolvedWuuHome, userHome string) (credentialstore.Store, error) {
	if base == nil || !filepath.IsAbs(resolvedWuuHome) || !filepath.IsAbs(userHome) {
		return nil, errors.New("native credentials require a selected backend and resolved Wuu/user homes")
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(userHome, ".codex")
	}
	grokHome := strings.TrimSpace(os.Getenv("GROK_HOME"))
	if grokHome == "" {
		grokHome = filepath.Join(userHome, ".grok")
	}
	return &nativeStore{Store: base, legacyPath: filepath.Join(resolvedWuuHome, "auth.json"), codexPath: filepath.Join(codexHome, "auth.json"), grokPath: filepath.Join(grokHome, "auth.json")}, nil
}

// Only the model document needs the legacy import. Unrelated capabilities and
// explicit model credentials must not open or mutate saved model accounts.
func (s *nativeStore) migrate(ctx context.Context, namespace, key string) error {
	if namespace != ModelCredentialNamespace || key != ModelCredentialKey {
		return nil
	}
	s.migrationMu.Lock()
	defer s.migrationMu.Unlock()
	if s.migrated {
		return nil
	}
	raw, err := s.Store.Get(ctx, ModelCredentialNamespace, ModelCredentialKey)
	defer clear(raw)
	if err != nil && !errors.Is(err, credentialstore.ErrNotFound) {
		return ErrUnavailable
	}
	current, err := decodeBoundFile(raw)
	if err != nil {
		return err
	}
	if !current.LegacyImported {
		previous, err := New(s.legacyPath).Load()
		if err != nil && !errors.Is(err, ErrNotFound) {
			return errors.New("legacy credential source is invalid or unavailable")
		}
		if err = s.Store.Update(ctx, ModelCredentialNamespace, ModelCredentialKey, func(raw []byte) ([]byte, error) {
			file, err := decodeBoundFile(raw)
			if err != nil {
				return nil, err
			}
			if file.LegacyImported {
				return raw, nil
			}
			for name, credentials := range previous.Providers {
				if _, exists := file.Providers[name]; !exists {
					file.Providers[name] = credentials
				}
			}
			file.LegacyImported = true
			return json.Marshal(file)
		}); err != nil {
			return errors.New("legacy credential migration failed; selected state may already be committed")
		}
	}
	s.migrated = true
	return nil
}

func (s *nativeStore) Get(ctx context.Context, namespace, key string) ([]byte, error) {
	if namespace != CLIRecordNamespace {
		if err := s.migrate(ctx, namespace, key); err != nil {
			return nil, err
		}
		return s.Store.Get(ctx, namespace, key)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var credentials Credentials
	var err error
	switch key {
	case "codex":
		credentials, err = readCodexCLI(s.codexPath)
	case "grok":
		credentials, err = readGrokCLI(s.grokPath)
	default:
		return nil, credentialstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(credentials)
}
func (s *nativeStore) Set(ctx context.Context, namespace, key string, value []byte) error {
	if namespace == CLIRecordNamespace {
		return errors.New("CLI credential records are read-only")
	}
	if err := s.migrate(ctx, namespace, key); err != nil {
		return err
	}
	return s.Store.Set(ctx, namespace, key, value)
}
func (s *nativeStore) Delete(ctx context.Context, namespace, key string) error {
	if namespace == CLIRecordNamespace {
		return errors.New("CLI credential records are read-only")
	}
	if err := s.migrate(ctx, namespace, key); err != nil {
		return err
	}
	return s.Store.Delete(ctx, namespace, key)
}
func (s *nativeStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	if namespace == CLIRecordNamespace {
		return errors.New("CLI credential records are read-only")
	}
	if err := s.migrate(ctx, namespace, key); err != nil {
		return err
	}
	return s.Store.Update(ctx, namespace, key, update)
}
func readCodexCLI(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, credentialstore.ErrNotFound
		}
		return Credentials{}, errors.New("Codex CLI credential file is unavailable")
	}
	defer clear(data)
	var raw struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &raw) != nil || strings.TrimSpace(raw.Tokens.AccessToken) == "" {
		return Credentials{}, errors.New("Codex CLI credential file is invalid")
	}
	return Credentials{Type: "oauth", AccessToken: raw.Tokens.AccessToken, RefreshToken: raw.Tokens.RefreshToken, IDToken: raw.Tokens.IDToken, AccountID: raw.Tokens.AccountID, AuthMode: "chatgpt", Source: "codex-cli"}, nil
}
func readGrokCLI(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Credentials{}, credentialstore.ErrNotFound
		}
		return Credentials{}, errors.New("Grok CLI credential file is unavailable")
	}
	defer clear(data)
	var entries map[string]struct {
		Key         string `json:"key"`
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(data, &entries) != nil {
		return Credentials{}, errors.New("Grok CLI credential file is invalid")
	}
	for _, scope := range []string{grokbuildspec.OAuthCredentialScope, grokbuildspec.CredentialIssuer} {
		if entry, ok := entries[scope]; ok {
			for _, token := range []string{entry.Key, entry.Token, entry.AccessToken} {
				if token = strings.TrimSpace(token); token != "" {
					return Credentials{AccessToken: token, Source: "grok-cli"}, nil
				}
			}
			return Credentials{}, errors.New("Grok CLI credential token is empty")
		}
	}
	return Credentials{}, credentialstore.ErrNotFound
}
