package host

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

// StoredDevice is one paired phone as remembered by the host.
type StoredDevice struct {
	// DelegatedOnly keys have no ambient owner authority; selected resource policy grants exact actions.
	DelegatedOnly bool      `json:"delegated_only,omitempty"`
	Pub           string    `json:"pub"` // base64url Ed25519 public key
	Name          string    `json:"name,omitempty"`
	AddedAt       time.Time `json:"added_at"`
	// Push registration, set by the phone after pairing. The token is opaque
	// to the host; an empty token means push is not registered.
	PushToken    string    `json:"push_token,omitempty"`
	PushPlatform string    `json:"push_platform,omitempty"` // "ios" | "android"
	PushSetAt    time.Time `json:"push_set_at,omitzero"`
}

type storeFile struct {
	V        int                  `json:"v"`
	Account  *account.Credentials `json:"account,omitempty"`
	HostSeed string               `json:"host_seed"` // base64url Ed25519 seed
	HostName string               `json:"host_name,omitempty"`
	RelayURL string               `json:"relay_url,omitempty"`
	// Legacy readers treat every devices entry as an owner and ignore V.
	// Restricted enrollment must never be written into that collection.
	Devices          []StoredDevice `json:"devices,omitempty"`
	DelegatedDevices []StoredDevice `json:"delegated_devices,omitempty"`
}

// normalizeDevices upgrades the short-lived role-tagged format and validates
// one unambiguous authority per key. Collection placement is authoritative for
// restricted entries even when their redundant public role flag was omitted.
func (f *storeFile) normalizeDevices() (bool, error) {
	seen := make(map[string]bool, len(f.Devices)+len(f.DelegatedDevices))
	owners := make([]StoredDevice, 0, len(f.Devices))
	delegated := make([]StoredDevice, 0, len(f.DelegatedDevices))
	changed := false
	for _, device := range f.Devices {
		if seen[device.Pub] {
			return false, errors.New("duplicate paired device authority")
		}
		seen[device.Pub] = true
		if device.DelegatedOnly {
			delegated = append(delegated, device)
			changed = true
		} else {
			owners = append(owners, device)
		}
	}
	for _, device := range f.DelegatedDevices {
		if seen[device.Pub] {
			return false, errors.New("duplicate paired device authority")
		}
		seen[device.Pub] = true
		if !device.DelegatedOnly {
			device.DelegatedOnly = true
			changed = true
		}
		delegated = append(delegated, device)
	}
	f.Devices, f.DelegatedDevices = owners, delegated
	return changed, nil
}

// Store is the host-side credential file (remote.json in the wuu home). It
// holds the host's long-term identity, the relay it reports to, and the set
// of paired phones. The file is chmod 0600 like auth.json.
type Store struct {
	mu   sync.Mutex
	path string
	data storeFile
	id   *secure.Identity
}

// LoadOrCreateStore opens the store at path, minting a fresh host identity on
// first use.
func LoadOrCreateStore(path, hostName string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		seed, err := base64.RawURLEncoding.DecodeString(s.data.HostSeed)
		if err != nil {
			return nil, fmt.Errorf("decode host seed: %w", err)
		}
		s.id, err = secure.IdentityFromSeed(seed)
		if err != nil {
			return nil, err
		}
		changed, err := s.data.normalizeDevices()
		if err != nil {
			return nil, fmt.Errorf("decode paired device authorities: %w", err)
		}
		if s.data.HostName == "" && hostName != "" {
			s.data.HostName = hostName
			changed = true
		}
		// Persist migration before returning any authority. Failure must not
		// leave the caller using a format that a downgraded host would widen.
		if changed {
			if err := s.save(); err != nil {
				return nil, err
			}
		}
		return s, nil
	case os.IsNotExist(err):
		id, err := secure.NewIdentity()
		if err != nil {
			return nil, err
		}
		s.id = id
		s.data = storeFile{
			V:        1,
			HostSeed: base64.RawURLEncoding.EncodeToString(id.Seed()),
			HostName: hostName,
		}
		if err := s.save(); err != nil {
			return nil, err
		}
		return s, nil
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
}

func (s *Store) Identity() *secure.Identity { return s.id }

func (s *Store) HostName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.HostName
}

func (s *Store) RelayURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.RelayURL
}

func (s *Store) SetRelayURL(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.RelayURL = url
	return s.saveLocked()
}

func (s *Store) SetHostName(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.HostName = name
	return s.saveLocked()
}

// AddDevice records a paired phone.
func (s *Store) AddDevice(pub []byte, name string, now time.Time) error {
	return s.addDevice(pub, name, now, false)
}

// AddDelegatedDevice enrolls a restricted controller without an owner grant.
func (s *Store) AddDelegatedDevice(pub []byte, name string, now time.Time) error {
	return s.addDevice(pub, name, now, true)
}

func (s *Store) addDevice(pub []byte, name string, now time.Time, delegated bool) error {
	key := secure.EncodeKey(pub)
	s.mu.Lock()
	defer s.mu.Unlock()
	if device := s.deviceLocked(key); device != nil {
		if device.DelegatedOnly != delegated {
			return errors.New("pairing cannot change an existing device authority role; revoke and enroll a new key")
		}
		device.Name = name
		return s.saveLocked()
	}
	device := StoredDevice{Pub: key, Name: name, AddedAt: now.UTC(), DelegatedOnly: delegated}
	if delegated {
		s.data.DelegatedDevices = append(s.data.DelegatedDevices, device)
	} else {
		s.data.Devices = append(s.data.Devices, device)
	}
	return s.saveLocked()
}

// RemoveDevice forgets a paired phone.
func (s *Store) RemoveDevice(pub string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	remove := func(device StoredDevice) bool { return device.Pub == pub }
	s.data.Devices = slices.DeleteFunc(s.data.Devices, remove)
	s.data.DelegatedDevices = slices.DeleteFunc(s.data.DelegatedDevices, remove)
	return s.saveLocked()
}

// SetDevicePushToken records the push registration for a paired device.
// An empty token unregisters push. Pub is the encoded (base64url) key as
// listed by Devices; unknown devices are an error so a phone cannot park a
// token on a pairing that no longer exists.
func (s *Store) SetDevicePushToken(pub, token, platform string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if device := s.deviceLocked(pub); device != nil {
		if token == "" {
			device.PushToken = ""
			device.PushPlatform = ""
			device.PushSetAt = time.Time{}
		} else {
			device.PushToken = token
			device.PushPlatform = platform
			device.PushSetAt = now.UTC()
		}
		return s.saveLocked()
	}
	return fmt.Errorf("device %s is not paired", pub)
}

// DevicePush returns the registered push token and platform for a device.
// ok is false when the device is unknown or push is not registered.
func (s *Store) DevicePush(pub string) (token, platform string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if device := s.deviceLocked(pub); device != nil && device.PushToken != "" {
		return device.PushToken, device.PushPlatform, true
	}
	return "", "", false
}

// IsPaired reports whether a device public key belongs to a paired phone.
func (s *Store) IsPaired(pub []byte) bool {
	key := secure.EncodeKey(pub)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceLocked(key) != nil
}

// deviceLocked finds either enrollment class; callers must hold s.mu. The
// returned pointer is never exposed beyond that critical section.
func (s *Store) deviceLocked(pub string) *StoredDevice {
	for i := range s.data.Devices {
		if s.data.Devices[i].Pub == pub {
			return &s.data.Devices[i]
		}
	}
	for i := range s.data.DelegatedDevices {
		if s.data.DelegatedDevices[i].Pub == pub {
			return &s.data.DelegatedDevices[i]
		}
	}
	return nil
}

// Devices returns a copy of the paired device list.
func (s *Store) Devices() []StoredDevice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(append([]StoredDevice(nil), s.data.Devices...), s.data.DelegatedDevices...)
}

func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return errors.New("store path is empty")
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), securefs.DirMode); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Account() *account.Credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Account == nil {
		return nil
	}
	c := *s.data.Account
	return &c
}
func (s *Store) SetAccount(c *account.Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Account = c
	return s.saveLocked()
}

// DevicePrincipal must only be called after cryptographic handshake verified
// this device key. The current persisted role is never taken from RPC params.
func (s *Store) DevicePrincipal(pub string) (principal.Verified, error) {
	s.mu.Lock()
	device := s.deviceLocked(pub)
	found, delegated := device != nil, false
	if found {
		delegated = device.DelegatedOnly
	}
	s.mu.Unlock()
	if !found {
		return principal.Verified{}, errors.New("device is not paired")
	}
	valid := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		device := s.deviceLocked(pub)
		return device != nil && device.DelegatedOnly == delegated
	}
	issuer := "wuu:paired:" + secure.EncodeKey(s.id.Public())
	if delegated {
		return principal.AuthenticatedWithValidity(principal.Identity{Kind: "device", Issuer: issuer, Tenant: "paired", Subject: pub}, valid)
	}
	return principal.PairedOwner(issuer, pub, valid)
}
