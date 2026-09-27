package appserver

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// Failure cases: resizing must not replace source bytes; invalid batches must
// not leave partial files; expiry must not touch history, moved destinations,
// or symlink targets; forks must own their paths independently of the source.
func TestInputImagesPreserveReceivedBytesAndForkOwnership(t *testing.T) {
	stateDir := t.TempDir()
	srv := &Server{rt: &runtime.Session{StateDir: stateDir}}
	raw := encodeTestJPEG(t, 2200, 1, 90)
	input := []TurnStartImage{{Data: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw)}}
	images, err := normalizeTurnStartImages(input)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := srv.userMessageWithInputImages("source", "edit this", images, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, "source"), "input-images", "*"))
	if len(paths) != 1 || !strings.Contains(msg.Content, paths[0]) {
		t.Fatalf("missing path: %+v %v", msg, paths)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Equal(raw, data) {
		t.Fatalf("original bytes lost: %v", err)
	}
	if msg.Images[0].Width != 2048 || msg.DisplayContent != "edit this" {
		t.Fatalf("vision/display changed: %+v", msg)
	}
	history := []providers.ChatMessage{msg}
	if err := preserveForkArtifacts(stateDir, "source", "fork", history); err != nil {
		t.Fatal(err)
	}
	forkPath := filepath.Join(statepath.SessionArtifactDir(stateDir, "fork"), "input-images", filepath.Base(paths[0]))
	if !strings.Contains(history[0].Content, forkPath) || strings.Contains(history[0].Content, paths[0]) {
		t.Fatalf("fork retained source path: %s", history[0].Content)
	}
	if err := os.RemoveAll(statepath.SessionArtifactDir(stateDir, "source")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(forkPath); err != nil || !bytes.Equal(raw, data) {
		t.Fatalf("fork lost image: %v", err)
	}
	if err := maintainInputImageStorage(stateDir, time.Now().Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(forkPath); !os.IsNotExist(err) {
		t.Fatalf("expired image remains: %v", err)
	}
	if history[0].Images[0].Data != images[0].Data {
		t.Fatal("cleanup changed vision history")
	}
	if err := preserveForkArtifacts(stateDir, "fork", "expired-fork", history); err != nil {
		t.Fatalf("expired images must not block forks: %v", err)
	}
}

func TestInputImageCacheRejectsEscapeAndPartialBatch(t *testing.T) {
	stateDir := t.TempDir()
	srv := &Server{rt: &runtime.Session{StateDir: stateDir}}
	raw := encodeTestJPEG(t, 1, 1, 90)
	input := []TurnStartImage{{Data: base64.StdEncoding.EncodeToString(raw)}, {Data: "invalid"}}
	if _, err := srv.userMessageWithInputImages("batch", "", nil, nil, input); err == nil {
		t.Fatal("accepted invalid batch")
	}
	paths, _ := filepath.Glob(filepath.Join(statepath.SessionArtifactDir(stateDir, "batch"), "input-images", "*"))
	if len(paths) != 0 {
		t.Fatalf("partial batch leaked: %v", paths)
	}
	dir := statepath.SessionArtifactDir(stateDir, "escape")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "input-images")); err != nil {
		t.Skip(err)
	}
	if _, err := srv.userMessageWithInputImages("escape", "", nil, nil, input[:1]); err == nil {
		t.Fatal("accepted symlinked cache")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside cache: %v %v", entries, err)
	}
}
