package host

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
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

func TestStoreDevicePushMissing(t *testing.T) {
	store, pub := makeStore(t, "test-phone")
	// Never registered: should return ok=false.
	_, _, ok := store.DevicePush(pub)
	if ok {
		t.Errorf("DevicePush: want ok=false on fresh device, got true")
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
