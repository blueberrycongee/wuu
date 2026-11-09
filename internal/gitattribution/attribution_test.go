package gitattribution

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestAddToCommitArgsHandlesGlobalOptionsAndPathspecs(t *testing.T) {
	input := []string{"-C", "/repo", "-c", "user.useConfigOnly=true", "commit", "-m", "message", "--", "file.txt"}
	got, added := AddToCommitArgs(input)
	if !added {
		t.Fatal("expected attribution to be added")
	}
	want := []string{
		"-C", "/repo", "-c", "user.useConfigOnly=true",
		"-c", "trailer.Co-authored-by.ifexists=addIfDifferent",
		"commit", "-m", "message",
		"--trailer", Trailer,
		"--", "file.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestAddToCommitArgsSkipsNonCommitAndAmend(t *testing.T) {
	for _, input := range [][]string{
		{"status", "--short"},
		{"-C", "/repo", "commit", "--amend", "-m", "message"},
		{"commit", "--ame", "-m", "message"},
	} {
		got, added := AddToCommitArgs(input)
		if added {
			t.Fatalf("unexpected attribution for %#v: %#v", input, got)
		}
		if !reflect.DeepEqual(got, input) {
			t.Fatalf("skipped args changed: got %#v want %#v", got, input)
		}
	}
}

func TestAddToCommitArgsDoesNotTreatOptionValuesAsAmend(t *testing.T) {
	for _, input := range [][]string{
		{"commit", "-m", "--amend"},
		{"commit", "--message", "--amend"},
		{"commit", "-F", "--amend"},
		{"commit", "--file", "--amend"},
		{"commit", "--author", "--amend", "-m", "message"},
		{"commit", "--pathspec-from-file", "--amend", "-m", "message"},
	} {
		got, added := AddToCommitArgs(input)
		if !added {
			t.Fatalf("expected attribution for option value in %#v", input)
		}
		if !containsArgPair(got, "--trailer", Trailer) {
			t.Fatalf("attribution missing from %#v", got)
		}
	}
}

func TestAddToCommitArgsDeduplicatesNonAdjacentTrailer(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "tester")
	runGit(t, root, "config", "user.email", "test@example.com")
	runShell(t, root, "printf x > file.txt")
	runGit(t, root, "add", "file.txt")

	args, added := AddToCommitArgs([]string{
		"commit", "-m", "subject", "-m",
		"Co-Authored-By: WUU Agent <" + Email + ">\nCo-authored-by: Other Agent <other@example.com>",
	})
	if !added {
		t.Fatal("expected attribution to be added")
	}
	runGit(t, root, args...)
	message := runGit(t, root, "log", "-1", "--format=%B")
	if count := strings.Count(message, Email); count != 1 {
		t.Fatalf("WUU trailer count = %d, want 1:\n%s", count, message)
	}
}

func containsArgPair(args []string, key, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key && args[index+1] == value {
			return true
		}
	}
	return false
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func runShell(t *testing.T, dir, command string) {
	t.Helper()
	cmd := exec.Command("bash", "-lc", command)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash %q: %v\n%s", command, err, output)
	}
}
