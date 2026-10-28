package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
)

// makeStore returns a Store backed by a temp file, with one device already
// paired. Each test gets its own store so the file-based persistence stays
// isolated and the temp dir is cleaned up by t.TempDir().
//
// The second return value is the encoded (base64url) device pub the
// store uses to identify the device — pass that to SetDevicePushToken
// and DevicePush, not the raw bytes, because the pairing path stores
// the encoded form.
func makeStore(t *testing.T, devName string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := LoadOrCreateStore(filepath.Join(dir, "remote.json"), "test-host")
	if err != nil {
		t.Fatalf("LoadOrCreateStore: %v", err)
	}
	// 32-byte fake Ed25519 public key; the actual cryptographic content
	// does not matter for store tests because the store only encodes
	// whatever bytes it gets.
	pub := makeKeyBytes(32)
	if err := store.AddDevice(pub, devName, time.Now()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	devices := store.Devices()
	if len(devices) != 1 {
		t.Fatalf("AddDevice: want 1 device, got %d", len(devices))
	}
	return store, devices[0].Pub
}

func makeKeyBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

func TestStoreSetAndGetPushToken(t *testing.T) {
	store, pub := makeStore(t, "test-phone")
	now := time.Now()
	if err := store.SetDevicePushToken(pub, "ExponentPushToken[abc123]", "ios", now); err != nil {
		t.Fatalf("SetDevicePushToken: %v", err)
	}
	token, platform, ok := store.DevicePush(pub)
	if !ok {
		t.Fatalf("DevicePush: want ok, got false")
	}
	if token != "ExponentPushToken[abc123]" {
		t.Errorf("DevicePush: want token=ExponentPushToken[abc123], got %q", token)
	}
	if platform != "ios" {
		t.Errorf("DevicePush: want platform=ios, got %q", platform)
	}
	// Persistence: reload from disk and check the token round-tripped.
	path := store.path
	reloaded, err := LoadOrCreateStore(path, "test-host")
	if err != nil {
		t.Fatalf("LoadOrCreateStore reload: %v", err)
	}
	rt, rp, ok := reloaded.DevicePush(pub)
	if !ok || rt != "ExponentPushToken[abc123]" || rp != "ios" {
		t.Errorf("reloaded: want ok token=ios, got ok=%v token=%q platform=%q", ok, rt, rp)
	}
}

func TestStoreSetPushTokenUnknownDevice(t *testing.T) {
	store, _ := makeStore(t, "test-phone")
	err := store.SetDevicePushToken("not-a-pub", "token", "android", time.Now())
	if err == nil {
		t.Fatalf("SetDevicePushToken(unknown): want error, got nil")
	}
}

func TestStoreSetPushTokenUnregister(t *testing.T) {
	store, pub := makeStore(t, "test-phone")
	if err := store.SetDevicePushToken(pub, "ExponentPushToken[xyz]", "android", time.Now()); err != nil {
		t.Fatalf("SetDevicePushToken: %v", err)
	}
	// Unregister by setting empty token.
	if err := store.SetDevicePushToken(pub, "", "", time.Now()); err != nil {
		t.Fatalf("SetDevicePushToken unregister: %v", err)
	}
	_, _, ok := store.DevicePush(pub)
	if ok {
		t.Errorf("DevicePush after unregister: want ok=false, got true")
	}
}

func TestStatusCreatedIdentityGetsItsComputerNameAtLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	initial, err := LoadOrCreateStore(path, "")
	if err != nil {
		t.Fatal(err)
	}
	original := initial.Identity().Public()
	named, err := LoadOrCreateStore(path, "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if named.HostName() != "Laptop" || !bytes.Equal(original, named.Identity().Public()) {
		t.Fatal("naming changed the identity or kept it anonymous")
	}
	reopened, err := LoadOrCreateStore(path, "Renamed OS host")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.HostName() != "Laptop" {
		t.Fatal("existing display name was overwritten")
	}
}

// A delegated key is authenticated but never an owner credential. Persisted
// role survives reopening, re-pairing cannot widen it, and revocation invalidates
// already captured identities instead of waiting for a transport reconnect.
func TestStoreDelegatedPairingAuthority(t *testing.T) {
	store, err := LoadOrCreateStore(filepath.Join(t.TempDir(), "remote.json"), "host")
	if err != nil {
		t.Fatal(err)
	}
	key := makeKeyBytes(32)
	if err = store.AddDelegatedDevice(key, "cloud-agent", time.Now()); err != nil {
		t.Fatal(err)
	}
	pub := store.Devices()[0].Pub
	identity, err := store.DevicePrincipal(pub)
	if err != nil || !identity.Valid() || identity.IsOwner() {
		t.Fatalf("delegated authority=%+v err=%v", identity.Identity(), err)
	}
	if err = store.AddDevice(key, "controller", time.Now()); err == nil {
		t.Fatal("re-pairing widened delegated authority")
	}
	reopened, err := LoadOrCreateStore(store.path, "host")
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.DevicePrincipal(pub)
	if err != nil || persisted.IsOwner() || !persisted.Valid() {
		t.Fatal("delegated role did not survive restart")
	}
	if err = store.RemoveDevice(pub); err != nil {
		t.Fatal(err)
	}
	if identity.Check(context.Background()) == nil {
		t.Fatal("revoked device retained authority")
	}
	ownerStore, ownerPub := makeStore(t, "owner")
	owner, err := ownerStore.DevicePrincipal(ownerPub)
	if err != nil || !owner.IsOwner() {
		t.Fatal("existing owner pairing compatibility lost")
	}
}

// This is the frozen 00c2811007094c80f8b6d4f32d0c4c5ebd9a5d04 reader shape,
// not the current StoredDevice type. That reader ignores unknown JSON fields
// and V, validates HostSeed, then considers every Devices entry a paired owner.
// A version bump or an unknown per-device role flag therefore cannot protect it.
type frozenOwnerDevice struct {
	Pub          string    `json:"pub"`
	Name         string    `json:"name,omitempty"`
	AddedAt      time.Time `json:"added_at"`
	PushToken    string    `json:"push_token,omitempty"`
	PushPlatform string    `json:"push_platform,omitempty"`
	PushSetAt    time.Time `json:"push_set_at,omitzero"`
}
type frozenOwnerStore struct {
	V        int                  `json:"v"`
	Account  *account.Credentials `json:"account,omitempty"`
	HostSeed string               `json:"host_seed"`
	HostName string               `json:"host_name,omitempty"`
	RelayURL string               `json:"relay_url,omitempty"`
	Devices  []frozenOwnerDevice  `json:"devices,omitempty"`
}

func frozenOwnerReader(t *testing.T, path string) frozenOwnerStore {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old frozenOwnerStore
	if err = json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	seed, err := base64.RawURLEncoding.DecodeString(old.HostSeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = secure.IdentityFromSeed(seed); err != nil {
		t.Fatal(err)
	}
	return old
}
func (old frozenOwnerStore) isPaired(pub []byte) bool {
	key := secure.EncodeKey(pub)
	for _, device := range old.Devices {
		if device.Pub == key {
			return true
		}
	}
	return false
}

func TestStoreDelegatedEnrollmentCannotBecomeFrozenOwner(t *testing.T) {
	store, ownerPub := makeStore(t, "owner")
	ownerKey := makeKeyBytes(32)
	delegatedKey := bytes.Repeat([]byte{99}, 32)
	delegatedPub := secure.EncodeKey(delegatedKey)
	if err := store.AddDelegatedDevice(delegatedKey, "delegate", time.Now()); err != nil {
		t.Fatal(err)
	}
	old := frozenOwnerReader(t, store.path)
	if old.isPaired(delegatedKey) {
		t.Fatal("frozen reader widened restricted enrollment into owner authority")
	}
	if !old.isPaired(ownerKey) {
		t.Fatal("ordinary pairing disappeared from frozen reader")
	}
	reopened, err := LoadOrCreateStore(store.path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Devices()) != 2 || !reopened.IsPaired(delegatedKey) || !reopened.IsPaired(ownerKey) {
		t.Fatal("current listing or encrypted handshake lost a pairing")
	}
	snapshot := reopened.Devices()
	snapshot[0].Name = "modified copy"
	if reopened.Devices()[0].Name == "modified copy" {
		t.Fatal("listing exposed mutable store state")
	}
	delegate, err := reopened.DevicePrincipal(delegatedPub)
	if err != nil || delegate.IsOwner() {
		t.Fatal("reopened restricted authority widened")
	}
	owner, err := reopened.DevicePrincipal(ownerPub)
	if err != nil || !owner.IsOwner() {
		t.Fatal("reopened owner role changed")
	}
	if err = reopened.AddDevice(delegatedKey, "owner", time.Now()); err == nil {
		t.Fatal("restricted key upgraded through re-pair")
	}
	if err = reopened.AddDelegatedDevice(ownerKey, "delegate", time.Now()); err == nil {
		t.Fatal("owner key changed role through re-pair")
	}
	if err = reopened.AddDelegatedDevice(delegatedKey, "renamed delegate", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = reopened.SetDevicePushToken(delegatedPub, "synthetic-token", "ios", time.Now()); err != nil {
		t.Fatal(err)
	}
	if token, platform, ok := reopened.DevicePush(delegatedPub); !ok || token != "synthetic-token" || platform != "ios" {
		t.Fatal("restricted push metadata was lost")
	}
	persisted, err := LoadOrCreateStore(store.path, "")
	if err != nil {
		t.Fatal(err)
	}
	if token, _, ok := persisted.DevicePush(delegatedPub); !ok || token != "synthetic-token" {
		t.Fatal("restricted metadata did not persist")
	}
	if err = reopened.RemoveDevice(delegatedPub); err != nil {
		t.Fatal(err)
	}
	if delegate.Check(context.Background()) == nil || reopened.IsPaired(delegatedKey) {
		t.Fatal("revoked restricted identity remained live")
	}
	if err = owner.Check(context.Background()); err != nil {
		t.Fatal("restricted revoke removed owner authority")
	}
	persisted, err = LoadOrCreateStore(store.path, "")
	if err != nil || persisted.IsPaired(delegatedKey) || !persisted.IsPaired(ownerKey) {
		t.Fatal("restricted revoke did not persist independently")
	}
}

func TestStoreMigratesTaggedDelegatesBeforeReturningAuthority(t *testing.T) {
	store, ownerPub := makeStore(t, "owner")
	delegatedKey := bytes.Repeat([]byte{77}, 32)
	delegatedPub := secure.EncodeKey(delegatedKey)
	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]json.RawMessage
	if err = json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	var devices []StoredDevice
	if err = json.Unmarshal(before["devices"], &devices); err != nil {
		t.Fatal(err)
	}
	devices = append(devices, StoredDevice{Pub: delegatedPub, Name: "old restricted", AddedAt: time.Now().UTC(), DelegatedOnly: true, PushToken: "retained-token", PushPlatform: "ios"})
	before["devices"], _ = json.Marshal(devices)
	before["v"] = json.RawMessage(`987`)
	raw, _ = json.Marshal(before)
	if err = os.WriteFile(store.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// Demonstrate the historical reader's unsafe semantics, including ignored V.
	if !frozenOwnerReader(t, store.path).isPaired(delegatedKey) {
		t.Fatal("fixture no longer reproduces the frozen downgrade hazard")
	}
	migrated, err := LoadOrCreateStore(store.path, "")
	if err != nil {
		t.Fatal(err)
	}
	if frozenOwnerReader(t, store.path).isPaired(delegatedKey) {
		t.Fatal("migration returned before restricted key left legacy owner collection")
	}
	if len(migrated.Devices()) != 2 {
		t.Fatal("migration lost enrollment")
	}
	p, err := migrated.DevicePrincipal(delegatedPub)
	if err != nil || p.IsOwner() {
		t.Fatal("migration widened delegate")
	}
	if token, _, ok := migrated.DevicePush(delegatedPub); !ok || token != "retained-token" {
		t.Fatal("migration lost restricted metadata")
	}
	p, err = migrated.DevicePrincipal(ownerPub)
	if err != nil || !p.IsOwner() {
		t.Fatal("migration damaged existing owner")
	}
	if !bytes.Equal(store.Identity().Public(), migrated.Identity().Public()) {
		t.Fatal("migration rotated host identity")
	}
	raw, err = os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	if err = json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	var restricted []StoredDevice
	if err = json.Unmarshal(after["delegated_devices"], &restricted); err != nil || len(restricted) != 1 || restricted[0].Pub != delegatedPub {
		t.Fatal("separate restricted collection not persisted")
	}
	reopened, err := LoadOrCreateStore(store.path, "")
	if err != nil || !reopened.IsPaired(delegatedKey) {
		t.Fatal("migrated enrollment could not reopen")
	}
}

func TestStoreRejectsDuplicateDeviceAuthoritiesWithoutRewrite(t *testing.T) {
	for _, scenario := range []string{"owner-and-restricted", "owner-and-tagged", "restricted-twice", "owner-twice"} {
		t.Run(scenario, func(t *testing.T) {
			store, _ := makeStore(t, "owner")
			raw, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]json.RawMessage
			if err = json.Unmarshal(raw, &data); err != nil {
				t.Fatal(err)
			}
			var owners []StoredDevice
			if err = json.Unmarshal(data["devices"], &owners); err != nil {
				t.Fatal(err)
			}
			duplicate := owners[0]
			duplicate.DelegatedOnly = true
			switch scenario {
			case "owner-and-restricted":
				data["delegated_devices"], _ = json.Marshal([]StoredDevice{duplicate})
			case "owner-and-tagged":
				data["devices"], _ = json.Marshal(append(owners, duplicate))
			case "restricted-twice":
				delete(data, "devices")
				data["delegated_devices"], _ = json.Marshal([]StoredDevice{duplicate, duplicate})
			case "owner-twice":
				data["devices"], _ = json.Marshal(append(owners, owners[0]))
			}
			corrupt, _ := json.Marshal(data)
			if err = os.WriteFile(store.path, corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err = LoadOrCreateStore(store.path, ""); err == nil {
				t.Fatal("ambiguous duplicate authority loaded")
			}
			after, err := os.ReadFile(store.path)
			if err != nil || !bytes.Equal(corrupt, after) {
				t.Fatal("rejected authority file was rewritten")
			}
		})
	}
}
