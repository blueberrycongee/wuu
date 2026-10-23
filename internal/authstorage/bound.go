package authstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

const ModelCredentialNamespace = "wuu.model-auth.v1"
const ModelCredentialKey = "providers"
const CLIRecordNamespace = "wuu.external-cli.v1"

var ErrUnavailable = errors.New("selected model credential provider is unavailable")

type boundFile struct {
	File
	// The marker and imported entries commit together. Deleted entries must
	// never be resurrected from the retained legacy recovery file.
	LegacyImported bool `json:"legacy_imported,omitempty"`
}

// NewBound adapts one admitted credential source; it never opens local storage.
// Keep ctx and the owning service generation alive for the complete operation.
func NewBound(ctx context.Context, selected credentialstore.Store) *Store {
	return &Store{bound: true, ctx: ctx, selected: selected}
}

func decodeBoundFile(raw []byte) (boundFile, error) {
	if raw == nil {
		return boundFile{File: emptyFile()}, nil
	}
	var file boundFile
	if json.Unmarshal(raw, &file) != nil || file.Version != CurrentVersion {
		return boundFile{}, errors.New("selected model credential document is invalid")
	}
	if file.Providers == nil {
		file.Providers = map[string]Credentials{}
	}
	return file, nil
}

func (s *Store) loadBound() (File, error) {
	if s.selected == nil {
		return File{}, ErrUnavailable
	}
	raw, err := s.selected.Get(s.ctx, ModelCredentialNamespace, ModelCredentialKey)
	defer clear(raw)
	if errors.Is(err, credentialstore.ErrNotFound) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, ErrUnavailable
	}
	file, err := decodeBoundFile(raw)
	return file.File, err
}

func (s *Store) updateBound(update func(*File)) error {
	if s.selected == nil {
		return ErrUnavailable
	}
	err := s.selected.Update(s.ctx, ModelCredentialNamespace, ModelCredentialKey, func(raw []byte) ([]byte, error) {
		file, err := decodeBoundFile(raw)
		if err != nil {
			return nil, err
		}
		update(&file.File)
		return json.Marshal(file)
	})
	if err != nil {
		return fmt.Errorf("%w; update may already be applied", ErrUnavailable)
	}
	return nil
}

// ReadCLI reads an explicitly selected external CLI account through the same
// credential service. Only the native adapter below interprets local files.
func ReadCLI(ctx context.Context, selected credentialstore.Store, cli string) (Credentials, error) {
	if selected == nil {
		return Credentials{}, ErrUnavailable
	}
	raw, err := selected.Get(ctx, CLIRecordNamespace, cli)
	defer clear(raw)
	if errors.Is(err, credentialstore.ErrNotFound) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, ErrUnavailable
	}
	var credentials Credentials
	if json.Unmarshal(raw, &credentials) != nil {
		return Credentials{}, errors.New("selected CLI credential record is invalid")
	}
	return credentials, nil
}
