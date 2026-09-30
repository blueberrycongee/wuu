package codemode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"testing/fstest"
)

// Packaging corruption must fail before any program or tool call can run.
func TestRuntimeWasmChunkIntegrity(t *testing.T) {
	data := make([]byte, 65536+7)
	for i := range data {
		data[i] = byte(i)
	}
	digest := sha256.Sum256(data)
	manifest, err := json.Marshal(map[string]any{"size": len(data), "sha256": hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	fixture := func() fstest.MapFS {
		return fstest.MapFS{
			"vendor/quickjs/wasm.json":        &fstest.MapFile{Data: append([]byte(nil), manifest...)},
			"vendor/quickjs/quickjs.wasm.000": &fstest.MapFile{Data: append([]byte(nil), data[:65536]...)},
			"vendor/quickjs/quickjs.wasm.001": &fstest.MapFile{Data: append([]byte(nil), data[65536:]...)},
		}
	}
	reconstructed, err := loadRuntimeWasm(fixture())
	if err != nil || sha256.Sum256(reconstructed) != digest {
		t.Fatalf("valid chunks: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(fstest.MapFS)
	}{
		{"missing manifest", func(f fstest.MapFS) { delete(f, "vendor/quickjs/wasm.json") }},
		{"invalid manifest", func(f fstest.MapFS) { f["vendor/quickjs/wasm.json"].Data = []byte(`{`) }},
		{"invalid size", func(f fstest.MapFS) { f["vendor/quickjs/wasm.json"].Data = []byte(`{"size":-1}`) }},
		{"excessive size", func(f fstest.MapFS) {
			f["vendor/quickjs/wasm.json"].Data = []byte(fmt.Sprintf(`{"size":%d}`, maxFrameBytes+1))
		}},
		{"invalid digest", func(f fstest.MapFS) { f["vendor/quickjs/wasm.json"].Data = []byte(`{"size":65543,"sha256":"invalid"}`) }},
		{"missing chunk", func(f fstest.MapFS) { delete(f, "vendor/quickjs/quickjs.wasm.001") }},
		{"extra chunk", func(f fstest.MapFS) { f["vendor/quickjs/quickjs.wasm.002"] = &fstest.MapFile{Data: []byte{1}} }},
		{"wrong chunk name", func(f fstest.MapFS) {
			f["vendor/quickjs/quickjs.wasm.002"] = f["vendor/quickjs/quickjs.wasm.001"]
			delete(f, "vendor/quickjs/quickjs.wasm.001")
		}},
		{"short chunk", func(f fstest.MapFS) { f["vendor/quickjs/quickjs.wasm.000"].Data = data[:65535] }},
		{"oversized chunk", func(f fstest.MapFS) { f["vendor/quickjs/quickjs.wasm.001"].Data = make([]byte, 65537) }},
		{"corrupt chunk", func(f fstest.MapFS) { f["vendor/quickjs/quickjs.wasm.000"].Data[1] ^= 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture()
			tc.mutate(f)
			if _, err := loadRuntimeWasm(f); err == nil {
				t.Fatal("corrupt runtime accepted")
			}
		})
	}
}
