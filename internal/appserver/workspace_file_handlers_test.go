package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/executionworker"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/workspaceview"
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
		chunk, err := workspaceview.ReadChunk(root, workspaceview.Params{Path: "download.bin", Offset: offset, Version: version})
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
	if _, err := workspaceview.ReadChunk(root, workspaceview.Params{Path: "download.bin", Offset: 256 * 1024, Version: version}); err == nil {
		t.Fatal("accepted changed file")
	}
	outside := t.TempDir()
	writeWorkspaceViewFile(t, outside, "secret", "outside")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(rt.RootDir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []workspaceview.Params{{Path: "escape"}, {Path: "../secret"}, {Path: "download.bin", Offset: -1}, {Path: "download.bin", Offset: 1}} {
		if _, err := workspaceview.ReadChunk(root, p); err == nil {
			t.Fatalf("accepted invalid download: %+v", p)
		}
	}
	out := &lockedBuffer{}
	response := workspaceViewRequest(t, New(rt, out), out, MethodWorkspaceFileChunk, workspaceview.Params{Path: "download.bin"})
	if response["error"] != nil || remarshal[workspaceview.ChunkResult](t, response["result"]).Size != int64(len(content)) {
		t.Fatal("chunk RPC unavailable")
	}
}

func workspaceViewRequest(t *testing.T, srv *Server, out *lockedBuffer, method string, params workspaceview.Params) map[string]any {
	t.Helper()
	id := fmt.Sprintf("workspace-%d", len(parseOutput(t, out.String())))
	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, response := range parseOutput(t, out.String()) {
			if response["id"] == id {
				return response
			}
		}
		select {
		case <-deadline:
			t.Fatal("workspace response timed out")
		case <-ticker.C:
		}
	}
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
	if os.Getenv("WUU_WORKSPACE_VIEW_WORKER") == "1" {
		if err := os.WriteFile(os.Getenv("WUU_WORKSPACE_VIEW_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(1)
		}
		if err := executionworker.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	{
		t.Setenv("WUU_WORKSPACE_VIEW_WORKER", "1")
		pidFile := filepath.Join(t.TempDir(), "worker.pid")
		t.Setenv("WUU_WORKSPACE_VIEW_PID", pidFile)
		rt := newTestRuntime(t, &fakeClient{})
		rt.WuuHome, rt.StateDir = t.TempDir(), t.TempDir()
		kit, err := tools.New(rt.RootDir)
		if err != nil {
			t.Fatal(err)
		}
		rt.Toolkit = kit
		defer rt.Cleanup()
		out := &lockedBuffer{}
		srv := New(rt, out)
		defer srv.Close()
		id := "execution-files"
		th := newThreadState(id, nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
		srv.threads[id] = th
		host := "HOST sentinel must not appear"
		writeWorkspaceViewFile(t, rt.RootDir, "same.txt", host)
		workerRoot := t.TempDir()
		pinDir := statepath.SessionArtifactDir(rt.StateDir, id)
		if err := os.MkdirAll(pinDir, 0700); err != nil {
			t.Fatal(err)
		}
		profile := executionenv.Profile{Backend: "command", Command: []string{os.Args[0], "-test.run=^TestWorkspaceViewBrowsesFilesAndResolvesChatReferences$"}, Workspace: workerRoot}
		data, _ := json.Marshal(executionenv.Selection{Name: "isolated", Profile: &profile})
		if err := os.WriteFile(filepath.Join(pinDir, "execution-environment.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		summary, err := rt.ExecutionEnvironmentSummary(id, th.CWD)
		if err != nil {
			t.Fatal(err)
		}
		target := &workspaceview.Target{ThreadID: id, EnvironmentID: summary.ID}
		params := workspaceview.Params{ThreadID: id, Target: target, Root: rt.RootDir, Path: "same.txt"}
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
			t.Fatal("cold inspection opened a worker or fell back to host", response)
		}
		if _, err := os.Stat(filepath.Join(rt.WuuHome, "execution-environments")); !os.IsNotExist(err) {
			t.Fatal("inspection created execution state", err)
		}
		thread, err := rt.NewThreadRuntime(id)
		if err != nil {
			t.Fatal(err)
		}
		write := func(content string) {
			t.Helper()
			args, _ := json.Marshal(map[string]string{"path": "same.txt", "content": content})
			if _, err := thread.Toolkit.Execute(context.Background(), providers.ToolCall{ID: "edit-file", Name: "write_file", Arguments: string(args)}); err != nil {
				t.Fatal(err)
			}
		}
		write("WORKER edited bytes")
		response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params)
		if response["error"] != nil {
			t.Fatal(response)
		}
		file := remarshal[workspaceview.FileResult](t, response["result"])
		if file.Text == nil || *file.Text != "WORKER edited bytes" || file.Root != workerRoot || file.Target == nil || file.Target.EnvironmentID != summary.ID || file.Target.ConnectionID == "" {
			t.Fatalf("wrong destination: %+v", file)
		}
		originalTarget := *file.Target
		// Relative and absolute agent links resolve at the worker root.
		params.Path = filepath.Join(workerRoot, "same.txt")
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] != nil {
			t.Fatal(response)
		}
		params.Path = ""
		params.Reference = "same.txt:2"
		resolved := remarshal[workspaceview.ReferenceResult](t, workspaceViewRequest(t, srv, out, MethodWorkspaceFileResolve, params)["result"])
		if resolved.AbsolutePath != filepath.Join(workerRoot, "same.txt") {
			t.Fatal("reference resolved to host", resolved)
		}
		params.Reference = ""
		listing := remarshal[workspaceview.DirectoryResult](t, workspaceViewRequest(t, srv, out, MethodWorkspaceDirectoryList, params)["result"])
		if listing.Root != workerRoot || len(listing.Entries) != 1 {
			t.Fatal("wrong directory", listing)
		}
		// The same bound channel transfers binary media in bounded revision-checked chunks.
		media := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0, 1, 2, 3}, 600000)...)
		if err := os.WriteFile(filepath.Join(workerRoot, "large.pdf"), media, 0600); err != nil {
			t.Fatal(err)
		}
		params.Path = "large.pdf"
		preview := remarshal[workspaceview.FileResult](t, workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params)["result"])
		if !preview.Binary || preview.RenderableKind != "pdf" || preview.RenderableURL != "" {
			t.Fatal("bad media preview", preview)
		}
		params.Version = preview.Version
		params.Target = preview.Target
		var downloaded []byte
		for params.Offset < int64(len(media)) {
			response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileChunk, params)
			if response["error"] != nil {
				t.Fatal(response)
			}
			chunk := remarshal[workspaceview.ChunkResult](t, response["result"])
			bytes, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil || len(bytes) > 256*1024 || chunk.Target.ConnectionID != originalTarget.ConnectionID {
				t.Fatal("invalid chunk", chunk, err)
			}
			downloaded = append(downloaded, bytes...)
			params.Offset += int64(len(bytes))
		}
		if !bytes.Equal(downloaded, media) {
			t.Fatal("media changed during transfer")
		}
		params.Offset = 0
		params.Version = ""
		params.Path = "same.txt"
		params.Target = target
		outside := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workerRoot, "escape")); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"../same.txt", outside, "escape"} {
			params.Path = path
			if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
				t.Fatalf("escaped worker: %q %+v", path, response)
			}
		}
		params.Path = "same.txt"
		params.Root = t.TempDir()
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
			t.Fatal("stale host workspace accepted", response)
		}
		params.Root = rt.RootDir
		params.Target = &workspaceview.Target{ThreadID: "another", EnvironmentID: summary.ID}
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
			t.Fatal("cross-session target accepted", response)
		}
		params.Target = &workspaceview.Target{ThreadID: id, EnvironmentID: "local"}
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
			t.Fatal("stale local tab silently retargeted", response)
		}
		params.Target = target
		// Retire the real transport. Reading cannot reconnect it or disclose host bytes.
		pidBytes, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(pidBytes))
		if err != nil {
			t.Fatal(err)
		}
		worker, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Kill(); err != nil {
			t.Fatal(err)
		}
		// The next read observes EOF and cannot start a replacement process.
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
			t.Fatal("disconnected read fell back", response)
		}
		// Only another explicit tool execution reconnects. Old chunk handles remain invalid.
		write("WORKER after reconnect")
		params.Target = &originalTarget
		if response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params); response["error"] == nil {
			t.Fatal("retired connection accepted", response)
		}
		params.Target = target
		response = workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, params)
		if response["error"] != nil {
			t.Fatal(response)
		}
		after := remarshal[workspaceview.FileResult](t, response["result"])
		if after.Text == nil || *after.Text != "WORKER after reconnect" {
			t.Fatal("reconnect did not read worker", after)
		}
		hostBytes, _ := os.ReadFile(filepath.Join(rt.RootDir, "same.txt"))
		if string(hostBytes) != host {
			t.Fatal("host file was changed")
		}
	}
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	writeWorkspaceViewFile(t, rt.RootDir, "src/main.go", "package main\n")
	writeWorkspaceViewFile(t, rt.RootDir, "README.md", "# Project\n")
	writeWorkspaceViewFile(t, rt.RootDir, "node_modules/ignore.js", "ignored")
	listed := workspaceViewRequest(t, srv, out, MethodWorkspaceDirectoryList, workspaceview.Params{})
	if listed["error"] != nil {
		t.Fatalf("list error: %v", listed["error"])
	}
	result := remarshal[workspaceview.DirectoryResult](t, listed["result"])
	paths := map[string]string{}
	for _, entry := range result.Entries {
		paths[entry.Path] = entry.Kind
	}
	if paths["src/"] != "directory" || paths["README.md"] != "file" || paths["node_modules/"] != "" {
		t.Fatalf("unexpected directory: %+v", result)
	}
	read := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceview.Params{Path: "src/main.go"})
	file := remarshal[workspaceview.FileResult](t, read["result"])
	if file.Text == nil || *file.Text != "package main\n" || file.Binary || file.Truncated || file.SHA256 == "" {
		t.Fatalf("unexpected preview: %+v, error: %v", file, read["error"])
	}
	for _, reference := range []string{"main.go", "`src/main.go:12:4`", filepath.Join(result.Root, "src/main.go") + " (lines 2-4)"} {
		response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileResolve, workspaceview.Params{Reference: reference})
		resolved := remarshal[workspaceview.ReferenceResult](t, response["result"])
		if resolved.Status != "resolved" || resolved.Path != "src/main.go" {
			t.Fatalf("resolve %q: %+v", reference, resolved)
		}
	}
	writeWorkspaceViewFile(t, rt.RootDir, "other/main.go", "package other\n")
	response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileResolve, workspaceview.Params{Reference: "main.go:1"})
	ambiguous := remarshal[workspaceview.ReferenceResult](t, response["result"])
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
	for _, params := range []workspaceview.Params{
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
		response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceview.Params{Root: root, Path: "file.txt"})
		if response["error"] != nil {
			t.Fatalf("known root %q rejected: %v", root, response["error"])
		}
		file := remarshal[workspaceview.FileResult](t, response["result"])
		if file.Text == nil || *file.Text != "from the selected root" {
			t.Fatalf("wrong workspace data: %+v", file)
		}
	}
}

func TestWorkspaceViewBoundsTextAndReturnsMediaThroughTheProtocol(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	writeWorkspaceViewFile(t, rt.RootDir, "large.txt", strings.Repeat("a", workspaceview.PreviewBytes+1))
	response := workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceview.Params{Path: "large.txt"})
	file := remarshal[workspaceview.FileResult](t, response["result"])
	if !file.Truncated || file.Text == nil || len(*file.Text) != workspaceview.PreviewBytes {
		t.Fatalf("preview was not bounded: %+v", file.SizeBytes)
	}
	writeWorkspaceViewFile(t, rt.RootDir, "tiny.gif", "GIF89a\x00\x00")
	response = workspaceViewRequest(t, srv, out, MethodWorkspaceFileRead, workspaceview.Params{Path: "tiny.gif"})
	file = remarshal[workspaceview.FileResult](t, response["result"])
	if file.RenderableKind != "image" || !strings.HasPrefix(file.RenderableURL, "data:image/gif;base64,") || !file.Binary || file.Text != nil {
		t.Fatalf("media must travel through the encrypted response, not a local URL: %+v", file)
	}
}
