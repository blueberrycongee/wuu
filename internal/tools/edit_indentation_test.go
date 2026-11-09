package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileIndentationFailureAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, actualIndent, submittedIndent string
	}{
		{"extra tab", "\t\t", "\t\t\t"},
		{"spaces for tab", "\t", "    "},
		{"mixed indentation", " \t ", "\t  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			kit, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "source.txt")
			body := "call() | value"
			original := "header\n" + tc.actualIndent + body + "\nfooter\n"
			mustWriteFile(t, path, original)
			edit := NewEditFileTool(kit.env)
			_, err = edit.Execute(context.Background(), mustMarshalMap(map[string]any{
				"path": "source.txt", "old_text": tc.submittedIndent + body, "new_text": "changed",
			}))
			if err == nil || !strings.Contains(err.Error(), "indentation_mismatch") {
				t.Fatalf("missing indentation diagnostic: %v", err)
			}
			for _, prefix := range []string{tc.actualIndent, tc.submittedIndent} {
				if !strings.Contains(err.Error(), fmt.Sprintf("%q", prefix)) {
					t.Fatalf("diagnostic must expose exact whitespace bytes: %v", err)
				}
			}
			if mustReadFile(t, path) != original {
				t.Fatal("indentation mismatch must not modify the file")
			}
			raw, err := NewReadFileTool(kit.env).Execute(context.Background(), `{"path":"source.txt","offset":2,"limit":1}`)
			if err != nil {
				t.Fatal(err)
			}
			numbered := strings.TrimSuffix(parseOut(t, raw)["content"].(string), "\n")
			_, old, ok := strings.Cut(numbered, "|")
			if !ok || old != tc.actualIndent+body {
				t.Fatalf("read prefix contaminated source: %q", numbered)
			}
			_, err = edit.Execute(context.Background(), mustMarshalMap(map[string]any{
				"path": "source.txt", "old_text": old, "new_text": tc.actualIndent + "updated()",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if mustReadFile(t, path) != "header\n"+tc.actualIndent+"updated()\nfooter\n" {
				t.Fatal("exact retry changed unrelated content or indentation")
			}
		})
	}
}

func TestEditFileDoesNotMisdiagnoseChangedBodyAsIndentation(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "source.txt")
	original := "\tcall()\n\treturn current\n"
	mustWriteFile(t, path, original)
	_, err = NewEditFileTool(kit.env).Execute(context.Background(), mustMarshalMap(map[string]any{
		"path": "source.txt", "old_text": "\t\tcall()\n\t\treturn stale\n", "new_text": "changed\n",
	}))
	if err == nil || strings.Contains(err.Error(), "indentation_mismatch") {
		t.Fatalf("must not suggest an indentation-only fix for stale code: %v", err)
	}
	if mustReadFile(t, path) != original {
		t.Fatal("stale edit modified the file")
	}
}
