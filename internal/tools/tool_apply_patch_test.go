package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestToolkit_ApplyPatchRejectsConflictingPathsAtomically(t *testing.T) {
	const updateAlpha = "*** Update File: a.txt\n@@\n-alpha\n+ALPHA\n"
	const moveA = "*** Update File: a.txt\n*** Move to: moved.txt\n"
	tests := []struct {
		name     string
		sections string
		conflict string
		symlink  bool
	}{
		{
			name:     "duplicate updates",
			sections: updateAlpha + "*** Update File: a.txt\n@@\n-beta\n+BETA\n",
			conflict: "a.txt",
		},
		{
			name:     "dot relative alias",
			sections: updateAlpha + "*** Update File: ./a.txt\n@@\n-beta\n+BETA\n",
			conflict: "a.txt",
		},
		{
			name:     "parent relative alias",
			sections: updateAlpha + "*** Update File: nested/../a.txt\n@@\n-beta\n+BETA\n",
			conflict: "a.txt",
		},
		{
			name:     "absolute alias",
			sections: updateAlpha + "*** Update File: $ROOT/a.txt\n@@\n-beta\n+BETA\n",
			conflict: "a.txt",
		},
		{
			name:     "symlink directory alias",
			sections: updateAlpha + "*** Update File: alias/a.txt\n@@\n-beta\n+BETA\n",
			conflict: "a.txt",
			symlink:  true,
		},
		{
			name:     "new file under symlink directory alias",
			sections: "*** Add File: new-dir/moved.txt\n+first\n*** Add File: alias/new-dir/moved.txt\n+second\n",
			conflict: "new-dir/moved.txt",
			symlink:  true,
		},
		{
			name:     "duplicate adds",
			sections: "*** Add File: moved.txt\n+first\n*** Add File: ./moved.txt\n+second\n",
			conflict: "moved.txt",
		},
		{
			name:     "duplicate deletes",
			sections: "*** Delete File: a.txt\n*** Delete File: ./a.txt\n",
			conflict: "a.txt",
		},
		{
			name:     "update then delete",
			sections: updateAlpha + "*** Delete File: ./a.txt\n",
			conflict: "a.txt",
		},
		{
			name:     "delete then update",
			sections: "*** Delete File: ./a.txt\n" + updateAlpha,
			conflict: "a.txt",
		},
		{
			name:     "update then move",
			sections: updateAlpha + moveA,
			conflict: "a.txt",
		},
		{
			name:     "move then update",
			sections: moveA + updateAlpha,
			conflict: "a.txt",
		},
		{
			name:     "duplicate move sources",
			sections: moveA + "*** Update File: ./a.txt\n*** Move to: moved-again.txt\n",
			conflict: "a.txt",
		},
		{
			name:     "duplicate move targets",
			sections: moveA + "*** Update File: b.txt\n*** Move to: ./moved.txt\n",
			conflict: "moved.txt",
		},
		{
			name:     "move then add target",
			sections: moveA + "*** Add File: ./moved.txt\n+replacement\n",
			conflict: "moved.txt",
		},
		{
			name:     "add then move target",
			sections: "*** Add File: ./moved.txt\n+replacement\n" + moveA,
			conflict: "moved.txt",
		},
	}
	for _, tt := range tests {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry_run=%t", tt.name, dryRun), func(t *testing.T) {
				root := t.TempDir()
				originals := map[string]string{
					"a.txt":     "alpha\nbeta\n",
					"b.txt":     "bravo\n",
					"other.txt": "untouched\n",
				}
				for path, content := range originals {
					mustWriteFile(t, filepath.Join(root, path), content)
				}
				if tt.symlink {
					if err := os.Symlink(root, filepath.Join(root, "alias")); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
				kit, err := New(root)
				if err != nil {
					t.Fatal(err)
				}
				kit.SetEditToolMode(EditToolModePatch)
				changedHookCalls := 0
				kit.SetOnFileChanged(func(string) { changedHookCalls++ })
				// Valid edits preceding the conflict must not reach disk, including
				// parent directories that rollback would otherwise leave behind.
				patch := "*** Begin Patch\n*** Update File: other.txt\n@@\n-untouched\n+changed\n*** Add File: created/new.txt\n+new\n" +
					strings.ReplaceAll(tt.sections, "$ROOT", filepath.ToSlash(root)) + "*** End Patch"
				args, err := json.Marshal(map[string]any{"patchText": patch, "dry_run": dryRun})
				if err != nil {
					t.Fatal(err)
				}
				result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
					Name: "apply_patch", Arguments: string(args),
				})
				if err == nil || !strings.Contains(err.Error(), "apply_patch verification failed") ||
					!strings.Contains(err.Error(), "multiple operations target") || !strings.Contains(err.Error(), tt.conflict) {
					t.Errorf("expected verification error identifying %s, got result=%s, err=%v", tt.conflict, result.TextProjection(), err)
				}
				if !result.IsError {
					t.Error("conflicting patch reported success")
				}
				for path, want := range originals {
					got, err := os.ReadFile(filepath.Join(root, path))
					if err != nil || string(got) != want {
						t.Errorf("conflicting patch changed %s: got %q, err=%v", path, got, err)
					}
				}
				for _, path := range []string{"created", "moved.txt", "moved-again.txt", "new-dir"} {
					if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
						t.Errorf("conflicting patch created %s: %v", path, err)
					}
				}
				if changedHookCalls != 0 {
					t.Errorf("conflicting patch fired %d file-change hooks", changedHookCalls)
				}
			})
		}
	}
}

func TestToolkit_ApplyPatchMultipleChunksPerFile(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetEditToolMode(EditToolModePatch)
	mustWriteFile(t, filepath.Join(root, "a.txt"), "alpha\nbeta\n")
	mustWriteFile(t, filepath.Join(root, "nested/a.txt"), "separate\n")
	patch := `*** Begin Patch
*** Update File: a.txt
@@
-alpha
+ALPHA
@@
-beta
+BETA
*** Update File: nested/a.txt
@@
-separate
+SEPARATE
*** End Patch`
	args, err := json.Marshal(map[string]any{"patchText": patch})
	if err != nil {
		t.Fatal(err)
	}
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
		Name: "apply_patch", Arguments: string(args),
	})
	if err != nil || result.IsError {
		t.Fatalf("non-conflicting patch failed: result=%s, err=%v", result.TextProjection(), err)
	}
	if got := mustReadFile(t, filepath.Join(root, "a.txt")); got != "ALPHA\nBETA\n" {
		t.Errorf("multiple chunks lost edits: %q", got)
	}
	if got := mustReadFile(t, filepath.Join(root, "nested/a.txt")); got != "SEPARATE\n" {
		t.Errorf("separate file with same basename lost edits: %q", got)
	}
}

func TestApplyPatchPreservesFileLineEndings(t *testing.T) {
	for _, fileEOL := range []string{"\n", "\r\n"} {
		for _, patchEOL := range []string{"\n", "\r\n"} {
			for _, trailing := range []bool{false, true} {
				for _, mode := range []string{"update", "move", "dry_run"} {
					t.Run(fmt.Sprintf("file=%q/patch=%q/trailing=%t/%s", fileEOL, patchEOL, trailing, mode), func(t *testing.T) {
						root, err := filepath.EvalSymlinks(t.TempDir())
						if err != nil {
							t.Fatal(err)
						}
						original := strings.Join([]string{"alpha", "", "middle", "beta"}, fileEOL)
						want := strings.Join([]string{"ALPHA", "extra", "", "middle", "BETA"}, fileEOL)
						if trailing {
							original += fileEOL
							want += fileEOL
						}
						path := filepath.Join(root, "a.txt")
						mustWriteFile(t, path, original)
						move := ""
						if mode == "move" {
							move = "*** Move to: moved.txt\n"
						}
						patch := "*** Begin Patch\n*** Update File: a.txt\n" + move + "@@\n-alpha\n+ALPHA\n+extra\n \n middle\n@@\n-beta\n+BETA\n*** End Patch"
						args, err := json.Marshal(map[string]any{"patchText": strings.ReplaceAll(patch, "\n", patchEOL), "dry_run": mode == "dry_run"})
						if err != nil {
							t.Fatal(err)
						}
						hooks := 0
						tool := NewApplyPatchTool(&Env{RootDir: root, OnFileChanged: func(string) { hooks++ }})
						if _, err := tool.Execute(context.Background(), string(args)); err != nil {
							t.Fatal(err)
						}
						if mode == "dry_run" {
							want = original
							if hooks != 0 {
								t.Fatalf("dry run fired %d hooks", hooks)
							}
						}
						if mode == "move" {
							if _, err := os.Stat(path); !os.IsNotExist(err) {
								t.Fatalf("move retained source: %v", err)
							}
							path = filepath.Join(root, "moved.txt")
						}
						if got := mustReadFile(t, path); got != want {
							t.Fatalf("file bytes = %q, want %q", got, want)
						}
					})
				}
			}
		}
	}
}

func TestApplyPatchCRLFRejectsInvalidAnchorsAtomically(t *testing.T) {
	for _, tc := range []struct{ name, old, kind string }{
		{"ambiguous", "same", "ambiguous_anchor"},
		{"stale", "missing", "anchor_not_found"},
		{"whitespace", " same", "anchor_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			original := "alpha\r\nsame\r\nmiddle\r\nsame\r\n"
			path := filepath.Join(root, "a.txt")
			mustWriteFile(t, path, original)
			patch := "*** Begin Patch\n*** Update File: a.txt\n@@\n-alpha\n+ALPHA\n@@\n-" + tc.old + "\n+changed\n*** End Patch"
			args, err := json.Marshal(map[string]string{"patchText": patch})
			if err != nil {
				t.Fatal(err)
			}
			hooks := 0
			tool := NewApplyPatchTool(&Env{RootDir: root, OnFileChanged: func(string) { hooks++ }})
			if _, err := tool.Execute(context.Background(), string(args)); err == nil || !strings.Contains(err.Error(), tc.kind) {
				t.Fatalf("expected %s, got %v", tc.kind, err)
			}
			if got := mustReadFile(t, path); got != original || hooks != 0 {
				t.Fatalf("failed patch changed file: %q, hooks=%d", got, hooks)
			}
		})
	}
}
