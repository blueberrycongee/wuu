package appserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitViewCommand(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func initGitViewRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitViewCommand(t, root, "init", "-b", "main")
	gitViewCommand(t, root, "config", "user.name", "Test")
	gitViewCommand(t, root, "config", "user.email", "test@example.invalid")
	return root
}

func TestWorkspaceGitReviewsCombinedChangesAndRenames(t *testing.T) {
	root := initGitViewRepo(t)
	writeWorkspaceViewFile(t, root, "modified.txt", "before\n")
	writeWorkspaceViewFile(t, root, "old\tname.txt", "rename me\n")
	writeWorkspaceViewFile(t, root, "deleted.txt", "delete me\n")
	gitViewCommand(t, root, "add", ".")
	gitViewCommand(t, root, "commit", "-m", "Initial")
	writeWorkspaceViewFile(t, root, "modified.txt", "staged\n")
	gitViewCommand(t, root, "add", "modified.txt")
	writeWorkspaceViewFile(t, root, "modified.txt", "staged\nworking\n")
	gitViewCommand(t, root, "mv", "old\tname.txt", "new\nname.txt")
	gitViewCommand(t, root, "rm", "deleted.txt")
	writeWorkspaceViewFile(t, root, "new file.txt", "new\n")
	ctx := context.Background()
	changes, err := readWorkspaceGitChanges(ctx, root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]workspaceGitChange{}
	for _, change := range changes.Files {
		byPath[change.Path] = change
	}
	if len(byPath) != 4 || byPath["new\nname.txt"].OldPath != "old\tname.txt" || byPath["modified.txt"].Additions != 2 || byPath["modified.txt"].Deletions != 1 || byPath["new file.txt"].Status != "untracked" {
		t.Fatalf("wrong changes: %+v", changes)
	}
	diff, err := readWorkspaceGitDiff(ctx, root, "HEAD", "modified.txt")
	if err != nil {
		t.Fatal(err)
	}
	if diff.OriginalText == nil || *diff.OriginalText != "before\n" || diff.ModifiedText == nil || *diff.ModifiedText != "staged\nworking\n" || !strings.Contains(diff.Patch, "+working") {
		t.Fatalf("wrong combined diff: %+v", diff)
	}
	renamed, err := readWorkspaceGitDiff(ctx, root, "HEAD", "new\nname.txt")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Status != "renamed" || renamed.OriginalText == nil || *renamed.OriginalText != "rename me\n" {
		t.Fatalf("wrong rename: %+v", renamed)
	}
	deleted, err := readWorkspaceGitDiff(ctx, root, "HEAD", "deleted.txt")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != "deleted" || deleted.ModifiedText == nil || *deleted.ModifiedText != "" {
		t.Fatalf("wrong deleted diff: %+v", deleted)
	}
	status, err := readWorkspaceGitStatus(ctx, root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "main" || status.Detached || status.DirtyCount != 4 || status.StagedDiff.Files != 3 {
		t.Fatalf("wrong status: %+v", status)
	}
}

func TestWorkspaceGitUnbornBinaryAndBoundedPreview(t *testing.T) {
	root := initGitViewRepo(t)
	writeWorkspaceViewFile(t, root, "first.txt", "first\n")
	gitViewCommand(t, root, "add", "first.txt")
	writeWorkspaceViewFile(t, root, "binary.dat", "binary\x00bytes")
	writeWorkspaceViewFile(t, root, "large.txt", strings.Repeat("line\n", workspacePreviewBytes))
	ctx := context.Background()
	baseline, err := workspaceGitBaseline(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := readWorkspaceGitStatus(ctx, root, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if status.DirtyCount != 3 || status.StagedDiff.Additions != 1 {
		t.Fatalf("wrong unborn status: %+v", status)
	}
	first, err := readWorkspaceGitDiff(ctx, root, baseline, "first.txt")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "added" || first.OriginalText == nil || *first.OriginalText != "" || first.ModifiedText == nil || *first.ModifiedText != "first\n" {
		t.Fatalf("wrong new file: %+v", first)
	}
	binary, err := readWorkspaceGitDiff(ctx, root, baseline, "binary.dat")
	if err != nil {
		t.Fatal(err)
	}
	if !binary.Binary || binary.ModifiedText != nil {
		t.Fatalf("wrong binary diff: %+v", binary)
	}
	large, err := readWorkspaceGitDiff(ctx, root, baseline, "large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !large.Truncated || len(large.Patch) > workspacePreviewBytes || large.ModifiedText == nil || len(*large.ModifiedText) > workspacePreviewBytes {
		t.Fatal("unbounded preview")
	}
}

func TestWorkspaceGitTreatsPathsLiterallyAndDoesNotExecuteDiffDrivers(t *testing.T) {
	root := initGitViewRepo(t)
	writeWorkspaceViewFile(t, root, "[file].txt", "before\n")
	writeWorkspaceViewFile(t, root, ".gitattributes", "*.txt diff=custom\n")
	gitViewCommand(t, root, "add", ".")
	gitViewCommand(t, root, "commit", "-m", "Initial")
	// A failing driver makes accidental execution observable without a shell payload.
	gitViewCommand(t, root, "config", "diff.custom.command", "false")
	gitViewCommand(t, root, "config", "diff.custom.textconv", "false")
	writeWorkspaceViewFile(t, root, "[file].txt", "after\n")
	outside := t.TempDir()
	writeWorkspaceViewFile(t, outside, "secret", "private content")
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	diff, err := readWorkspaceGitDiff(ctx, root, "HEAD", "[file].txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Patch, "+after") {
		t.Fatalf("literal path failed: %+v", diff)
	}
	link, err := readWorkspaceGitDiff(ctx, root, "HEAD", "link")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(link.Patch, "private content") || link.ModifiedText == nil || *link.ModifiedText != filepath.Join(outside, "secret") {
		t.Fatalf("followed link: %+v", link)
	}
	for _, path := range []string{"../secret", filepath.Join(outside, "secret"), "missing.txt"} {
		if _, err := readWorkspaceGitDiff(ctx, root, "HEAD", path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestWorkspaceGitRPCRejectsUnknownRootAndReportsNonRepository(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	for index, root := range []string{t.TempDir(), rt.RootDir} {
		params, err := json.Marshal(workspaceViewParams{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		id := json.RawMessage(`"git-view-` + string(rune('a'+index)) + `"`)
		if err := srv.handleWorkspaceGit(context.Background(), Request{ID: id, Method: MethodWorkspaceGitStatus, Params: params}); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), "git-view-"+string(rune('a'+index)))
		if index == 0 && response["error"] == nil {
			t.Fatal("accepted unknown root")
		}
		if index == 1 && (response["error"] != nil || remarshal[workspaceGitStatus](t, response["result"]).IsRepo) {
			t.Fatalf("non-repository: %+v", response)
		}
	}
}

func TestWorkspaceGitRPCReadsTheSelectedRepository(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	root := initGitViewRepo(t)
	rt.RootDir = root
	writeWorkspaceViewFile(t, root, "selected.txt", "selected workspace\n")
	out := &lockedBuffer{}
	srv := New(rt, out)
	for _, method := range []string{MethodWorkspaceGitStatus, MethodWorkspaceGitChanges, MethodWorkspaceGitDiff} {
		params, err := json.Marshal(workspaceViewParams{Root: root, Path: "selected.txt"})
		if err != nil {
			t.Fatal(err)
		}
		id, err := json.Marshal(method)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.handleWorkspaceGit(context.Background(), Request{ID: id, Method: method, Params: params}); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), method)
		if response["error"] != nil {
			t.Fatalf("%s: %v", method, response["error"])
		}
		switch method {
		case MethodWorkspaceGitStatus:
			status := remarshal[workspaceGitStatus](t, response["result"])
			if !status.IsRepo || status.Diff.Additions != 1 {
				t.Fatalf("wrong status: %+v", status)
			}
		case MethodWorkspaceGitChanges:
			changes := remarshal[workspaceGitChanges](t, response["result"])
			if len(changes.Files) != 1 || changes.Files[0].Path != "selected.txt" || changes.Files[0].Additions != 1 {
				t.Fatalf("wrong changes: %+v", changes)
			}
		case MethodWorkspaceGitDiff:
			diff := remarshal[workspaceGitDiff](t, response["result"])
			if diff.ModifiedText == nil || *diff.ModifiedText != "selected workspace\n" {
				t.Fatalf("wrong diff: %+v", diff)
			}
		}
	}
}
