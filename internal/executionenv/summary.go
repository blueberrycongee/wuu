package executionenv

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Summary is an observed execution destination, not a probe. It deliberately
// excludes transport commands, host credentials and forwarded environment.
type Summary struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Backend string `json:"backend,omitempty"`
	Root    string `json:"root,omitempty"`
	State   string `json:"state"`
}

// Summary reads the existing session pin without selecting an environment,
// starting a worker or changing retained resources.
func (m *Manager) Summary(store, session, artifactDir, localRoot string) (Summary, error) {
	data, err := os.ReadFile(filepath.Join(artifactDir, "execution-environment.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Summary{State: "not_initialized"}, nil
	}
	if err != nil {
		return Summary{}, errors.New("cannot read the session execution environment")
	}
	var selection Selection
	if json.Unmarshal(data, &selection) != nil {
		return Summary{}, errors.New("saved execution environment is invalid")
	}
	if selection.Name == "local" && selection.Profile == nil {
		return Summary{Name: "local", Backend: "local", Root: localRoot, State: "local"}, nil
	}
	if !profileName.MatchString(selection.Name) || selection.Profile == nil || selection.Profile.Validate() != nil {
		return Summary{}, errors.New("saved execution environment is invalid")
	}
	m.mu.Lock()
	environment := m.environments[filepath.Join(store, session)]
	m.mu.Unlock()
	if environment == nil {
		identity := Identity(store, session, selection.Name, *selection.Profile)
		environment = NewEnvironment(*selection.Profile, identity, session, filepath.Join(store, "execution-environments"))
	}
	result := environment.Summary()
	result.Name = selection.Name
	if result.State == "not_initialized" {
		if _, err := os.Stat(filepath.Join(store, "execution-environments", result.ID, "provisioned")); err == nil {
			result.State = "disconnected"
		} else if !errors.Is(err, os.ErrNotExist) {
			return Summary{}, errors.New("cannot read execution environment state")
		}
	}
	return result, nil
}

func (e *Environment) Summary() Summary {
	e.statusMu.RLock()
	state, client := e.status, e.statusClient
	e.statusMu.RUnlock()
	if state == "ready" && client != nil && client.Failed() {
		state = "disconnected"
	}
	return Summary{ID: e.identity, Backend: e.profile.Backend, Root: e.Root(), State: state}
}

func (e *Environment) setStatus(state string, client *Client) {
	e.statusMu.Lock()
	e.status, e.statusClient = state, client
	e.statusMu.Unlock()
}
