package plugin

import (
	"strings"
	"testing"
)

func TestStorageTransferManifestValidationAndFingerprint(t *testing.T) {
	valid := `{"schemaVersion":1,"id":"receiver","storageTransfers":{"imports":[{"id":"records-v1","plugin":"sender","scope":"user","domain":"private","keyPrefix":"records."}]}}`
	manifest, err := normalizeManifest([]byte(valid), t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := (Plugin{Manifest: manifest, Source: "user"}).PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	manifest.StorageTransfers.Imports[0].KeyPrefix = "other."
	second, err := (Plugin{Manifest: manifest, Source: "user"}).PackageContract()
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("transfer declaration omitted from fingerprint")
	}
	for _, bad := range []string{
		strings.Replace(valid, `"scope":"user"`, `"scope":"workspace"`, 1),
		strings.Replace(valid, `"keyPrefix":"records."`, `"keyPrefix":""`, 1),
		strings.Replace(valid, `"plugin":"sender"`, `"plugin":"receiver"`, 1),
		strings.Replace(valid, `"domain":"private"`, `"domain":""`, 1),
		strings.Replace(valid, `"scope":"user"`, `"scope":"user","unknown":true`, 1),
	} {
		if _, err := normalizeManifest([]byte(bad), t.TempDir(), false); err == nil {
			t.Fatalf("accepted invalid declaration: %s", bad)
		}
	}
}
