package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
)

// Use real porcelain output, including Git's two NUL-delimited rename paths.
// Identity is local to the fixture command; no user or repository config is changed.
func searchFixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Search Test", "GIT_AUTHOR_EMAIL=search@example.invalid", "GIT_COMMITTER_NAME=Search Test", "GIT_COMMITTER_EMAIL=search@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
	return string(out)
}

func searchFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	searchFixtureGit(t, root, "init", "-q")
	mustWriteFile(t, filepath.Join(root, "tracked.txt"), "base seed\n")
	searchFixtureGit(t, root, "add", "tracked.txt")
	searchFixtureGit(t, root, "commit", "-qm", "test: initialize search fixture")
	return root
}

func searchFixtureKit(t *testing.T, root, session string) *Toolkit {
	t.Helper()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(session)
	kit.SetToolSearchEnabled(false)
	kit.SetEditToolMode(EditToolModePatch)
	return kit
}

func searchFixtureCall(t *testing.T, kit *Toolkit, name string, args map[string]any) string {
	t.Helper()
	out, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: mustMarshalMap(args)})
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
	return out
}

type searchFixturePage struct {
	Total    int               `json:"total"`
	Revision string            `json:"workspace_revision"`
	Files    []string          `json:"files"`
	Matches  []grepMatch       `json:"matches"`
	Counts   []grepCountResult `json:"counts"`
	Page     struct {
		Next struct {
			Offset   int    `json:"offset"`
			Revision string `json:"expected_revision"`
		} `json:"next"`
	} `json:"page"`
}

func parseSearchFixturePage(t *testing.T, raw string) searchFixturePage {
	t.Helper()
	var page searchFixturePage
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return page
}

func (p searchFixturePage) paths() []string {
	paths := append([]string(nil), p.Files...)
	for _, m := range p.Matches {
		paths = append(paths, m.File)
	}
	for _, c := range p.Counts {
		paths = append(paths, c.File)
	}
	return paths
}

func TestSearchFreshFirstPage(t *testing.T) {
	for _, tc := range []struct {
		name, writer                     string
		untracked, large, rename, nonGit bool
	}{
		{name: "tracked-patch", writer: "apply_patch"},
		{name: "tracked-edit", writer: "edit_file"},
		{name: "untracked-write", writer: "write_file", untracked: true},
		{name: "external-unequal", writer: "external"},
		{name: "external-atomic-equal", writer: "atomic"},
		{name: "large-tail", writer: "atomic", large: true},
		{name: "real-rename", writer: "atomic", rename: true},
		{name: "non-git-large-tail", writer: "atomic", large: true, nonGit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if !tc.nonGit {
				root = searchFixtureRepo(t)
			} else {
				t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
			}
			path := "tracked.txt"
			if tc.untracked {
				path = "new/file.txt"
			}
			if tc.rename {
				path = "renamed ü space.txt"
				searchFixtureGit(t, root, "mv", "tracked.txt", path)
			}
			old, next := "violet seed\n", "silver leaf\n"
			if tc.large {
				prefix := strings.Repeat("p\n", (1<<20)/2)
				old, next = prefix+old, prefix+next
			}
			if tc.writer == "external" || tc.writer == "write_file" {
				next += "an extra line\n"
			}
			fullPath := filepath.Join(root, path)
			mustWriteFile(t, fullPath, old)
			status := ""
			if !tc.nonGit {
				status = searchFixtureGit(t, root, "status", "--porcelain=v2", "-z", "--branch")
				if tc.rename && (!strings.Contains(status, "2 RM ") || !strings.Contains(status, " "+path+"\x00tracked.txt\x00")) {
					t.Fatalf("missing real rename record: %q", status)
				}
			}
			session := t.TempDir()
			writer := searchFixtureKit(t, root, session)
			other := searchFixtureKit(t, root, t.TempDir())
			check := func(k *Toolkit, oldTotal, newTotal int) {
				t.Helper()
				for _, mode := range []string{"content", "count", "files_with_matches"} {
					for _, q := range []struct {
						pattern string
						total   int
					}{{"violet", oldTotal}, {"silver", newTotal}} {
						raw := searchFixtureCall(t, k, "grep", map[string]any{"path": path, "pattern": q.pattern, "output_mode": mode})
						if got := parseSearchFixturePage(t, raw).Total; got != q.total {
							t.Errorf("%s %s total=%d, want %d; raw=%s", mode, q.pattern, got, q.total, raw)
						}
					}
				}
			}
			check(writer, 1, 0)
			check(other, 1, 0)
			switch tc.writer {
			case "apply_patch":
				writer.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", true)
				searchFixtureCall(t, writer, "read_file", map[string]any{"path": path})
				searchFixtureCall(t, writer, "apply_patch", map[string]any{"patchText": "*** Begin Patch\n*** Update File: " + path + "\n@@\n-violet seed\n+silver leaf\n*** End Patch"})
			case "edit_file", "write_file":
				writer.SetEditToolMode(EditToolModeText)
				searchFixtureCall(t, writer, "read_file", map[string]any{"path": path})
				args := map[string]any{"path": path, "content": next}
				if tc.writer == "edit_file" {
					args = map[string]any{"path": path, "old_text": old, "new_text": next}
				}
				searchFixtureCall(t, writer, tc.writer, args)
			case "atomic":
				info, err := os.Stat(fullPath)
				if err != nil {
					t.Fatal(err)
				}
				mustWriteFile(t, fullPath+".replacement", next)
				if err := os.Chtimes(fullPath+".replacement", info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(fullPath+".replacement", fullPath); err != nil {
					t.Fatal(err)
				}
			default:
				mustWriteFile(t, fullPath, next)
			}
			if got := mustReadFile(t, fullPath); got != next {
				t.Fatal("writer did not update disk")
			}
			if !tc.nonGit && searchFixtureGit(t, root, "status", "--porcelain=v2", "-z", "--branch") != status {
				t.Fatal("fixture must preserve the same raw Git status")
			}
			// Reopen before either original instance can refresh its persistent cache.
			check(searchFixtureKit(t, root, session), 0, 1)
			check(other, 0, 1)
			check(writer, 0, 1)
			check(searchFixtureKit(t, root, ""), 0, 1)
		})
	}
}

func TestSearchFreshPageKeepsContinuationBound(t *testing.T) {
	for _, mode := range []string{"glob", "content", "count", "files_with_matches"} {
		t.Run(mode, func(t *testing.T) {
			root := searchFixtureRepo(t)
			n := grepPageSize + 5
			name, args := "grep", map[string]any{"path": "new", "pattern": "violet", "output_mode": mode}
			if mode == "glob" {
				n = globPageSize + 5
				name, args = "glob", map[string]any{"path": "new", "pattern": "*.txt"}
			}
			want := make([]string, n)
			for i := range want {
				want[i] = fmt.Sprintf("new/f%04d.txt", i)
				mustWriteFile(t, filepath.Join(root, want[i]), "violet seed\n")
			}
			session := t.TempDir()
			kit := searchFixtureKit(t, root, session)
			first := parseSearchFixturePage(t, searchFixtureCall(t, kit, name, args))
			if first.Total != n || first.Page.Next.Offset == 0 {
				t.Fatalf("first page: %+v", first)
			}
			status := searchFixtureGit(t, root, "status", "--porcelain=v2", "-z")
			mustWriteFile(t, filepath.Join(root, "new/000-added.txt"), "violet seed\n")
			if got := searchFixtureGit(t, root, "status", "--porcelain=v2", "-z"); got != status {
				t.Fatal("untracked directory status changed")
			}
			args["offset"], args["expected_revision"] = first.Page.Next.Offset, first.Page.Next.Revision
			second := parseSearchFixturePage(t, searchFixtureCall(t, kit, name, args))
			if got := append(first.paths(), second.paths()...); !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot lost or repeated records: %v", got)
			}
			for _, token := range []string{"", "wrong-token"} {
				args["expected_revision"] = token
				if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: mustMarshalMap(args)}); err == nil {
					t.Errorf("accepted invalid token %q", token)
				}
			}
			delete(args, "expected_revision")
			args["offset"] = 0
			fresh := parseSearchFixturePage(t, searchFixtureCall(t, kit, name, args))
			if fresh.Total != n+1 {
				t.Errorf("fresh total=%d, want %d", fresh.Total, n+1)
			}
			args["offset"], args["expected_revision"] = first.Page.Next.Offset, first.Page.Next.Revision
			if out, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: mustMarshalMap(args)}); err == nil || !strings.Contains(err.Error(), "stale") {
				t.Errorf("old token after refresh: err=%v out=%s", err, out)
			}
			args["offset"], args["expected_revision"] = fresh.Page.Next.Offset, fresh.Page.Next.Revision
			kit = searchFixtureKit(t, root, session)
			last := parseSearchFixturePage(t, searchFixtureCall(t, kit, name, args))
			if got := append(fresh.paths(), last.paths()...); !reflect.DeepEqual(got, append([]string{"new/000-added.txt"}, want...)) {
				t.Errorf("new snapshot lost or repeated records: %v", got)
			}
			mustWriteFile(t, filepath.Join(root, "top-level.txt"), "changed revision\n")
			if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: mustMarshalMap(args)}); err == nil {
				t.Error("accepted stale workspace revision")
			}
		})
	}
}

func TestSearchFreshPageWithoutRevisionOrRipgrep(t *testing.T) {
	withRGTestHooks(t, func(string) (string, error) { return "", exec.ErrNotFound }, nil)
	root := t.TempDir()
	kit := searchFixtureKit(t, root, t.TempDir())
	// A failed revision computation is represented by an empty context revision.
	ctx := context.Background()
	ctx = toolctx.WithWorkspaceRevision(ctx, kit.env.RevisionRoot(ctx), "")
	for _, mode := range []string{"content", "count", "files_with_matches", "glob"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(root, mode+".txt")
			mustWriteFile(t, path, "violet seed\n")
			var tool Tool = NewGrepTool(kit.env)
			args := map[string]any{"path": root, "include": mode + ".txt", "pattern": "violet", "output_mode": mode}
			if mode == "glob" {
				tool = NewGlobTool(kit.env)
				args = map[string]any{"pattern": mode + ".txt"}
			}
			call := func(want int) {
				raw, err := tool.Execute(ctx, mustMarshalMap(args))
				if err != nil {
					t.Fatal(err)
				}
				if got := parseSearchFixturePage(t, raw).Total; got != want {
					t.Errorf("total=%d, want %d; raw=%s", got, want, raw)
				}
			}
			call(1)
			if mode == "glob" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				mustWriteFile(t, path, "silver leaf\n")
			}
			call(0)
		})
	}
}

func TestSearchFreshPagePropagatesSearchFailure(t *testing.T) {
	if lookupRG() == "" {
		t.Skip("ripgrep is not available")
	}
	for _, mode := range []string{"content", "count", "files_with_matches", "glob"} {
		t.Run(mode, func(t *testing.T) {
			root := searchFixtureRepo(t)
			mustWriteFile(t, filepath.Join(root, "tracked.txt"), "violet seed\n")
			kit := searchFixtureKit(t, root, t.TempDir())
			name, args := "grep", map[string]any{"path": "tracked.txt", "pattern": "violet", "output_mode": mode}
			if mode == "glob" {
				name, args = "glob", map[string]any{"pattern": "*.txt"}
			}
			searchFixtureCall(t, kit, name, args)
			// Fail the search subprocess after warming the cache, without changing
			// the workspace revision. A cached success must not mask this failure.
			withRGTestHooks(t, exec.LookPath, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
				return exec.CommandContext(ctx, filepath.Join(root, "missing-rg"))
			})
			if out, err := kit.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: mustMarshalMap(args)}); err == nil {
				t.Errorf("new search returned cached success despite subprocess failure: %s", out)
			}
		})
	}
}

func TestSearchFreshLargePagesSurviveArtifactRecovery(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "active")
	for _, mode := range []string{"glob", "count", "files_with_matches"} {
		t.Run(mode, func(t *testing.T) {
			root := searchFixtureRepo(t)
			kit := searchFixtureKit(t, root, t.TempDir())
			want := make([]string, globPageSize+5)
			for i := range want {
				// Escaped paths force generic settlement even for a single search page.
				want[i] = fmt.Sprintf("new/f%04d-%s.txt", i, strings.Repeat("path&", 32))
				mustWriteFile(t, filepath.Join(root, want[i]), "violet seed\n")
			}
			name, args := "grep", map[string]any{"path": "new", "pattern": "violet", "output_mode": mode}
			if mode == "glob" {
				name, args = "glob", map[string]any{"path": "new", "pattern": "*.txt"}
			}
			sequence := 0
			execute := func(name string, args map[string]any) (string, error) {
				sequence++
				result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
					ID: fmt.Sprintf("large-search-%d", sequence), Name: name, Arguments: mustMarshalMap(args),
				})
				return providers.ProjectToolResult(result).ToolText, err
			}
			readPage := func(args map[string]any) (searchFixturePage, string) {
				t.Helper()
				raw, err := execute(name, args)
				if err != nil {
					t.Fatal(err)
				}
				envelope := parseOut(t, raw)
				if envelope["kind"] != "archived_tool_result" {
					return parseSearchFixturePage(t, raw), ""
				}
				artifact := envelope["artifact_ref"].(string)
				var recovered strings.Builder
				recovered.WriteString(envelope["preview_head"].(string))
				continuation := envelope["continuation"].(map[string]any)
				for pages := 0; continuation["has_more"] == true; pages++ {
					if pages > 100 {
						t.Fatal("artifact recovery did not finish")
					}
					raw, err := execute("read_file", continuation["next"].(map[string]any))
					if err != nil {
						t.Fatal(err)
					}
					part := parseOut(t, raw)
					if part["action"] != "read_bytes" || part["byte_offset"] != float64(recovered.Len()) {
						t.Fatalf("artifact recovery changed its range: %v", part)
					}
					content, _ := part["content"].(string)
					if len(content) == 0 || part["byte_count"] != float64(len(content)) {
						t.Fatal("recovery did not advance by the displayed bytes")
					}
					recovered.WriteString(content)
					continuation = part["continuation"].(map[string]any)
				}
				recovered.WriteString(envelope["preview_tail"].(string))
				if recovered.String() != mustReadFile(t, artifact) {
					t.Fatal("recovered search page lost or repeated archived bytes")
				}
				return parseSearchFixturePage(t, recovered.String()), artifact
			}
			first, artifact := readPage(args)
			if artifact == "" || first.Total != len(want) || first.Page.Next.Offset == 0 {
				t.Fatalf("fixture must archive a paginated search result: %+v", first)
			}
			oldArchive := mustReadFile(t, artifact)
			status := searchFixtureGit(t, root, "status", "--porcelain=v2", "-z")
			mustWriteFile(t, filepath.Join(root, "new/000-added.txt"), "violet seed\n")
			if searchFixtureGit(t, root, "status", "--porcelain=v2", "-z") != status {
				t.Fatal("fixture must retain the collapsed untracked status")
			}
			fresh, freshArtifact := readPage(args)
			if freshArtifact == "" || fresh.Total != len(want)+1 || fresh.Revision == first.Revision {
				t.Fatalf("archived first page did not refresh: %+v", fresh)
			}
			args["offset"], args["expected_revision"] = first.Page.Next.Offset, first.Page.Next.Revision
			if _, err := execute(name, args); err == nil || !strings.Contains(err.Error(), "stale") {
				t.Fatalf("old search token accepted after refreshing archived results: %v", err)
			}
			paths, page := fresh.paths(), fresh
			for page.Page.Next.Offset != 0 {
				if len(page.paths()) == 0 || len(paths) > len(want)+1 {
					t.Fatal("search continuation did not finish")
				}
				args["offset"], args["expected_revision"] = page.Page.Next.Offset, page.Page.Next.Revision
				page, _ = readPage(args)
				paths = append(paths, page.paths()...)
			}
			if !reflect.DeepEqual(paths, append([]string{"new/000-added.txt"}, want...)) {
				t.Fatal("fresh search pages lost or repeated records across artifact recovery")
			}
			if mustReadFile(t, artifact) != oldArchive {
				t.Fatal("refreshing search overwrote the original archived page")
			}
		})
	}
}
