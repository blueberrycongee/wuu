package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/session"
)

func TestWorkspaceDownloadChunksAndRejectsChangedOrEscapingFiles(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	content := strings.Repeat("0123456789", 70000)
	writeWorkspaceViewFile(t, rt.RootDir, "download.bin", content)
	root, err := os.OpenRoot(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var downloaded []byte
	version := ""
	for offset := int64(0); offset < int64(len(content)); {
		chunk, err := readWorkspaceChunk(root, workspaceViewParams{Path: "download.bin", Offset: offset, Version: version})
		if err != nil {
			t.Fatal(err)
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(data) > 256*1024 || chunk.Offset != offset {
			t.Fatal("invalid bounded chunk")
		}
		downloaded = append(downloaded, data...)
		offset += int64(len(data))
		version = chunk.Version
	}
	if string(downloaded) != content {
		t.Fatal("download differs from actual file")
	}
	changed := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(rt.RootDir, "download.bin"), changed, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkspaceChunk(root, workspaceViewParams{Path: "download.bin", Offset: 256 * 1024, Version: version}); err == nil {
		t.Fatal("accepted changed file")
	}
	outside := t.TempDir()
	writeWorkspaceViewFile(t, outside, "secret", "outside")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(rt.RootDir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []workspaceViewParams{{Path: "escape"}, {Path: "../secret"}, {Path: "download.bin", Offset: -1}, {Path: "download.bin", Offset: 1}} {
		if _, err := readWorkspaceChunk(root, p); err == nil {
			t.Fatalf("accepted invalid download: %+v", p)
		}
	}
	out := &lockedBuffer{}
	response := workspaceViewRequest(t, New(rt, out), out, MethodWorkspaceFileChunk, workspaceViewParams{Path: "download.bin"})
	if response["error"] != nil || remarshal[workspaceChunkResult](t, response["result"]).Size != int64(len(content)) {
		t.Fatal("chunk RPC unavailable")
	}
}

func workspaceViewRequest(t *testing.T, srv *Server, out *lockedBuffer, method string, params workspaceViewParams) map[string]any {
	t.Helper()
	id := method + params.Root + params.Path + params.Reference
	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	return responseByID(t, parseOutput(t, out.String()), id)
}

func writeWorkspaceViewFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceViewBrowsesFilesAndResolvesChatReferences(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	writeWorkspaceViewFile(t, rt.RootDir, "src/main.go", "package main\n")
	writeWorkspaceViewFile(t, rt.RootDir, "README.md", "# Project\n")
	writeWorkspaceViewFile(t, rt.RootDir, "node_modules/ignore.js", "ignored")
	listed := workspaceViewRequest(t, srv, out, MethodWorkspaceDirectoryList, workspaceViewParams{})
	if listed["error"] != nil {
		t.Fatalf("list error: %v", listed["error"])
	}
	result := remarshal[workspaceDirectoryResult](t, listed["result"])
	paths := map[string]string{}
	for _, entry := range result.Entries {
		paths[entry.Path] = entry.Kind
	}
	if paths["src/"] != "directory" || paths["README.md"] != "file" || paths["node_modules/"] != "" {
		t.Fatalf("unexpected directory: %+v", result)
	}
	read := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceViewParams{Path: "src/main.go"})
	file := remarshal[workspaceFileResult](t, read["result"])
	if file.Text == nil || *file.Text != "package main\n" || file.Binary || file.Truncated || file.SHA256 == "" {
		t.Fatalf("unexpected preview: %+v, error: %v", file, read["error"])
	}
	for _, reference := range []string{"main.go", "`src/main.go:12:4`", filepath.Join(result.Root, "src/main.go") + " (lines 2-4)"} {
		response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileResolve, workspaceViewParams{Reference: reference})
		resolved := remarshal[workspaceReferenceResult](t, response["result"])
		if resolved.Status != "resolved" || resolved.Path != "src/main.go" {
			t.Fatalf("resolve %q: %+v", reference, resolved)
		}
	}
	writeWorkspaceViewFile(t, rt.RootDir, "other/main.go", "package other\n")
	response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileResolve, workspaceViewParams{Reference: "main.go:1"})
	ambiguous := remarshal[workspaceReferenceResult](t, response["result"])
	if ambiguous.Status != "ambiguous" || len(ambiguous.Matches) != 2 {
		t.Fatalf("ambiguous basename: %+v", ambiguous)
	}
}

func TestWorkspaceViewRejectsTraversalAndUnknownRoots(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	outside := t.TempDir()
	writeWorkspaceViewFile(t, outside, "secret", "private")
	if err := os.Symlink(outside, filepath.Join(rt.RootDir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, params := range []workspaceViewParams{
		{Path: "../secret"}, {Path: "escape/secret"}, {Root: outside, Path: "secret"},
		{Root: filepath.Join(rt.RootDir, "escape"), Path: "secret"}, {Path: filepath.Join(outside, "secret")},
	} {
		response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params)
		if response["error"] == nil {
			t.Fatalf("accepted out-of-scope read: %+v", params)
		}
		if response["result"] != nil {
			t.Fatalf("leaked read result: %+v", response)
		}
	}
}

func TestWorkspaceViewAllowsRegisteredWorkspacesAndSessionWorktrees(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	registered, worktree := t.TempDir(), t.TempDir()
	data, err := json.Marshal(map[string]any{"projects": []map[string]string{{"id": "registered", "name": "Registered", "path": registered}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt.WuuHome, "projects.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, "worktree-thread", worktree); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	for _, root := range []string{registered, worktree} {
		writeWorkspaceViewFile(t, root, "file.txt", "from the selected root")
		response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceViewParams{Root: root, Path: "file.txt"})
		if response["error"] != nil {
			t.Fatalf("known root %q rejected: %v", root, response["error"])
		}
		file := remarshal[workspaceFileResult](t, response["result"])
		if file.Text == nil || *file.Text != "from the selected root" {
			t.Fatalf("wrong workspace data: %+v", file)
		}
	}
}

func TestWorkspaceViewBoundsTextAndReturnsMediaThroughTheProtocol(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	writeWorkspaceViewFile(t, rt.RootDir, "large.txt", strings.Repeat("a", workspacePreviewBytes+1))
	response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceViewParams{Path: "large.txt"})
	file := remarshal[workspaceFileResult](t, response["result"])
	if !file.Truncated || file.Text == nil || len(*file.Text) != workspacePreviewBytes {
		t.Fatalf("preview was not bounded: %+v", file.SizeBytes)
	}
	writeWorkspaceViewFile(t, rt.RootDir, "tiny.gif", "GIF89a\x00\x00")
	response = workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceViewParams{Path: "tiny.gif"})
	file = remarshal[workspaceFileResult](t, response["result"])
	if file.RenderableKind != "image" || !strings.HasPrefix(file.RenderableURL, "data:image/gif;base64,") || !file.Binary || file.Text != nil {
		t.Fatalf("media must travel through the encrypted response, not a local URL: %+v", file)
	}
}
