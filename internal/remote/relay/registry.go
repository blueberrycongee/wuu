package relay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// DeviceInfo describes one enrolled phone under an account.
type DeviceInfo struct {
	Name    string    `json:"name,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

type accountRecord struct {
	Devices map[string]DeviceInfo `json:"devices"`
}

type registryFile struct {
	V        int                       `json:"v"`
	Accounts map[string]*accountRecord `json:"accounts"`
}

// Registry is the relay's persistent view of who exists: accounts (keyed by
// the host's public key) and the phone devices each host has enrolled. It
// deliberately stores nothing else - no messages, no keys beyond the public
// ones used for routing.
//
// With an empty path the registry is memory-only (tests, throwaway relays).
type Registry struct {
	mu       sync.Mutex
	path     string
	accounts map[string]*accountRecord
}

func OpenRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, accounts: map[string]*accountRecord{}}
	if path == "" {
		return r, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read relay registry: %w", err)
	}
	var f registryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decode relay registry %s: %w", path, err)
	}
	if f.Accounts != nil {
		r.accounts = f.Accounts
	}
	for _, acct := range r.accounts {
		if acct.Devices == nil {
			acct.Devices = map[string]DeviceInfo{}
		}
	}
	return r, nil
}

// EnsureAccount creates the account on first host authentication.
func (r *Registry) EnsureAccount(hostPub string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.accounts[hostPub]; ok {
		return nil
	}
	r.accounts[hostPub] = &accountRecord{Devices: map[string]DeviceInfo{}}
	return r.saveLocked()
}

// AddDevice enrolls a phone under an account.
func (r *Registry) AddDevice(hostPub, devicePub, name string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.accounts[hostPub]
	if !ok {
		acct = &accountRecord{Devices: map[string]DeviceInfo{}}
		r.accounts[hostPub] = acct
	}
	acct.Devices[devicePub] = DeviceInfo{Name: name, AddedAt: now.UTC()}
	return r.saveLocked()
}

// RemoveDevice revokes a phone.
func (r *Registry) RemoveDevice(hostPub, devicePub string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.accounts[hostPub]
	if !ok {
		return nil
	}
	delete(acct.Devices, devicePub)
	return r.saveLocked()
}

// HasDevice reports whether devicePub is enrolled under hostPub.
func (r *Registry) HasDevice(hostPub, devicePub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.accounts[hostPub]
	if !ok {
		return false
	}
	_, ok = acct.Devices[devicePub]
	return ok
}

// AccountByDevice finds which account a phone belongs to. Device keys are
// random 32-byte values, so a device belongs to at most one account in
// practice; first match wins.
func (r *Registry) AccountByDevice(devicePub string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for hostPub, acct := range r.accounts {
		if _, ok := acct.Devices[devicePub]; ok {
			return hostPub, true
		}
	}
	return "", false
}

// Devices lists the devices enrolled under an account, sorted by name for
// stable output.
func (r *Registry) Devices(hostPub string) []struct {
	Pub  string
	Info DeviceInfo
} {
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.accounts[hostPub]
	if !ok {
		return nil
	}
	out := make([]struct {
		Pub  string
		Info DeviceInfo
	}, 0, len(acct.Devices))
	for pub, info := range acct.Devices {
		out = append(out, struct {
			Pub  string
			Info DeviceInfo
		}{pub, info})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Info.Name != out[j].Info.Name {
			return out[i].Info.Name < out[j].Info.Name
		}
		return out[i].Pub < out[j].Pub
	})
	return out
}

func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(registryFile{V: 1, Accounts: r.accounts}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), securefs.DirMode); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
