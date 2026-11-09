package executionenv

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

type Selection struct {
	Name    string   `json:"name"`
	Profile *Profile `json:"profile,omitempty"`
}

// Manager retains one connection per conversation across runtime/model reloads.
type Manager struct {
	mu           sync.Mutex
	environments map[string]*Environment
}

func NewManager() *Manager { return &Manager{environments: make(map[string]*Environment)} }

func (m *Manager) Open(store, session, artifactDir string, cfg Config) (*Environment, Selection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := cfg.Validate(); err != nil {
		return nil, Selection{}, err
	}
	file := filepath.Join(artifactDir, "execution-environment.json")
	var selected Selection
	data, err := os.ReadFile(file)
	if err == nil {
		if err = json.Unmarshal(data, &selected); err != nil {
			return nil, selected, err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		selected.Name = cfg.Default
		if selected.Name == "" {
			selected.Name = "local"
		}
		if selected.Name != "local" {
			p := cfg.Profiles[selected.Name]
			selected.Profile = &p
		}
		data, err = json.Marshal(selected)
		if err != nil {
			return nil, selected, err
		}
		if err = os.MkdirAll(artifactDir, 0700); err != nil {
			return nil, selected, err
		}
		if err = securefs.WriteFileAtomic(file, data); err != nil {
			return nil, selected, err
		}
	} else {
		return nil, selected, err
	}
	if selected.Name == "local" && selected.Profile == nil {
		return nil, selected, nil
	}
	if selected.Profile == nil {
		return nil, selected, errors.New("saved execution environment has no profile")
	}
	if err = selected.Profile.Validate(); err != nil {
		return nil, selected, err
	}
	key := filepath.Join(store, session)
	if environment := m.environments[key]; environment != nil {
		return environment, selected, nil
	}
	identity := Identity(store, session, selected.Name, *selected.Profile)
	environment := NewEnvironment(*selected.Profile, identity, session, filepath.Join(store, "execution-environments"))
	m.environments[key] = environment
	return environment, selected, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	environments := m.environments
	m.environments = make(map[string]*Environment)
	m.mu.Unlock()
	var err error
	for _, environment := range environments {
		err = errors.Join(err, environment.Close())
	}
	return err
}
