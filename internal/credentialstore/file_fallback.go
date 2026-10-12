package credentialstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Get(_ context.Context, namespace, key string) ([]byte, error) {
	values, err := s.load()
	if err != nil {
		return nil, err
	}
	encoded, ok := values[credentialAccount(namespace, key)]
	if !ok {
		return nil, ErrNotFound
	}
	value, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("credentialstore file decode: %w", err)
	}
	return value, nil
}

func (s *FileStore) Set(ctx context.Context, namespace, key string, value []byte) error {
	return s.mutate(ctx, func(values map[string]string) error {
		values[credentialAccount(namespace, key)] = base64.StdEncoding.EncodeToString(value)
		return nil
	})
}

func (s *FileStore) Delete(ctx context.Context, namespace, key string) error {
	return s.mutate(ctx, func(values map[string]string) error {
		delete(values, credentialAccount(namespace, key))
		return nil
	})
}

func (s *FileStore) Update(ctx context.Context, namespace, key string, update func([]byte) ([]byte, error)) error {
	return s.mutate(ctx, func(values map[string]string) error {
		account := credentialAccount(namespace, key)
		var current []byte
		if encoded, ok := values[account]; ok {
			var err error
			current, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return fmt.Errorf("credentialstore file decode: %w", err)
			}
		}
		next, err := update(bytes.Clone(current))
		if err != nil {
			return err
		}
		if (current == nil) == (next == nil) && bytes.Equal(current, next) {
			return nil
		}
		if next == nil {
			delete(values, account)
		} else {
			values[account] = base64.StdEncoding.EncodeToString(next)
		}
		return nil
	})
}

func (s *FileStore) mutate(ctx context.Context, mutate func(map[string]string) error) error {
	lock, err := acquireStoreLock(ctx, s.path+".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	values, err := s.load()
	if errors.Is(err, ErrNotFound) {
		values = map[string]string{}
	} else if err != nil {
		return err
	}
	if err := mutate(values); err != nil {
		return err
	}
	return s.write(values)
}

func (s *FileStore) load() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("credentialstore file read: %w", err)
	}
	values := map[string]string{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("credentialstore file parse: %w", err)
	}
	return values, nil
}

func (s *FileStore) write(values map[string]string) error {
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return fmt.Errorf("credentialstore file marshal: %w", err)
	}
	if err := securefs.WriteFileAtomic(s.path, append(data, '\n')); err != nil {
		return fmt.Errorf("credentialstore file write: %w", err)
	}
	return nil
}
