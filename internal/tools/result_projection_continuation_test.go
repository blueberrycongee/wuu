package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestRecordProjectorContinuationAdvancesByDisplayedRecords(t *testing.T) {
	makeRaw := func(offset, count int) string {
		files := make([]string, count)
		for i := range files {
			files[i] = fmt.Sprintf("file-%05d-%s.go", offset+i, strings.Repeat("path", 8))
		}
		return mustMarshalMap(map[string]any{
			"action":             "glob",
			"pattern":            "**/*.go",
			"workspace_revision": "revision-1",
			"offset":             offset,
			"has_more":           false,
			"files":              files,
		})
	}
	pc := projectorContext{CallID: "page", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/page.txt"}
	firstOut, _, ok := projectGlobResult(makeRaw(0, 1000), pc)
	if !ok {
		t.Fatal("first projection declined")
	}
	first := parseOut(t, firstOut)
	firstFiles := first["files"].([]any)
	next := first["page"].(map[string]any)["next"].(map[string]any)
	nextOffset := int(next["offset"].(float64))
	if nextOffset != len(firstFiles) || next["expected_revision"] != "revision-1" {
		t.Fatalf("cursor did not advance by displayed records: next=%+v displayed=%d", next, len(firstFiles))
	}

	secondOut, _, ok := projectGlobResult(makeRaw(nextOffset, 1000), pc)
	if !ok {
		t.Fatal("second projection declined")
	}
	second := parseOut(t, secondOut)
	secondFiles := second["files"].([]any)
	if len(secondFiles) == 0 || secondFiles[0] == firstFiles[len(firstFiles)-1] {
		t.Fatalf("second page repeated first-page evidence: first_last=%v second_first=%v", firstFiles[len(firstFiles)-1], secondFiles[0])
	}
}

func TestBoundedSearchProjectionUsesArtifactWithoutInventingContinuation(t *testing.T) {
	files := make([]string, 1000)
	for i := range files {
		files[i] = fmt.Sprintf("file-%05d-%s.go", i, strings.Repeat("path", 8))
	}
	raw := mustMarshalMap(map[string]any{
		"action":                 "glob",
		"pattern":                "**/*.go",
		"continuation_supported": false,
		"offset":                 0,
		"has_more":               true,
		"files":                  files,
	})
	out, _, ok := projectGlobResult(raw, projectorContext{
		CallID: "bounded", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/bounded.txt",
	})
	if !ok {
		t.Fatal("bounded projection declined")
	}
	parsed := parseOut(t, out)
	page := parsed["page"].(map[string]any)
	if _, exists := page["next"]; exists {
		t.Fatalf("bounded projection invented an unstable continuation: %+v", page)
	}
	projection := parsed["projection"].(map[string]any)
	recover, _ := projection["recover"].(string)
	if !strings.Contains(recover, "/s/bounded.txt") || !strings.Contains(recover, "narrow") {
		t.Fatalf("bounded projection recovery is not actionable: %+v", projection)
	}
}

func TestSearchHelpersSortFullSetBeforePaging(t *testing.T) {
	root := t.TempDir()
	for i := 519; i >= 0; i-- {
		mustWriteFile(t, filepath.Join(root, fmt.Sprintf("file-%03d.txt", i)), "needle\n")
	}
	files, err := globWithFallback(root, root, "**/*.txt", 0)
	if err != nil {
		t.Fatalf("glob fallback: %v", err)
	}
	if len(files) != 520 || files[0] != "file-000.txt" || files[519] != "file-519.txt" {
		t.Fatalf("glob did not return globally sorted full set: len=%d first=%q last=%q", len(files), files[0], files[len(files)-1])
	}
	first, more := pageWindow(files, 0, globPageSize)
	second, _ := pageWindow(files, len(first), globPageSize)
	if !more || len(first) != globPageSize || len(second) != 20 || first[len(first)-1] == second[0] {
		t.Fatalf("unstable page split: first=%d second=%d more=%v", len(first), len(second), more)
	}
}

func TestStableOffsetRejectsMissingOrChangedRevision(t *testing.T) {
	if err := validateStableOffset("glob", 10, "", "rev-1"); err == nil {
		t.Fatal("non-zero offset without revision was accepted")
	}
	if err := validateStableOffset("glob", 10, "rev-1", "rev-2"); err == nil {
		t.Fatal("stale revision was accepted")
	}
	if err := validateStableOffset("glob", 10, "rev-1", "rev-1"); err != nil {
		t.Fatalf("matching snapshot was rejected: %v", err)
	}
}

func TestContinuationSnapshotRevisionIncludesResultContent(t *testing.T) {
	first := continuationSnapshotRevision("git:same", "grep:content", []string{"match one"})
	second := continuationSnapshotRevision("git:same", "grep:content", []string{"match two"})
	if first == second {
		t.Fatal("result content changed without changing the continuation snapshot")
	}
}

func TestReadFileByteContinuationReturnsDisjointWindowsAndRejectsMutation(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("new toolkit: %v", err)
	}
	kit.env.SessionDir = t.TempDir()
	artifact := filepath.Join(kit.env.SessionDir, "tool-results", "blob.txt")
	mustWriteFile(t, artifact, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	tool := NewReadFileTool(kit.env)
	firstRaw, err := tool.Execute(context.Background(), fmt.Sprintf(`{"path":%q,"byte_range":{"offset":0,"limit":16}}`, artifact))
	if err != nil {
		t.Fatalf("first byte read: %v", err)
	}
	first := parseOut(t, firstRaw)
	next := first["continuation"].(map[string]any)["next"].(map[string]any)
	nextArgs, _ := json.Marshal(next)
	secondRaw, err := tool.Execute(context.Background(), string(nextArgs))
	if err != nil {
		t.Fatalf("second byte read: %v", err)
	}
	second := parseOut(t, secondRaw)
	if first["content"].(string)+second["content"].(string) != "0123456789abcdefghijklmnopqrstuv" {
		t.Fatalf("byte windows overlapped or skipped: first=%q second=%q", first["content"], second["content"])
	}
	if err := os.WriteFile(artifact, []byte("changed artifact content"), 0o644); err != nil {
		t.Fatalf("mutate artifact: %v", err)
	}
	if _, err := tool.Execute(context.Background(), string(nextArgs)); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("mutated artifact continuation was not rejected: %v", err)
	}
}

func TestReadFileProjectorPointsAtRemainingRequestedLines(t *testing.T) {
	rawText, _, _ := readFileEnvelope(5000)
	raw := parseOut(t, rawText)
	raw["content_sha256"] = "file-hash"
	pc := projectorContext{CallID: "read", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/read.txt"}
	out, _, ok := projectReadFileResult(mustMarshalMap(raw), pc)
	if !ok {
		t.Fatal("read_file projector declined")
	}
	m := parseOut(t, out)
	next := m["continuation"].(map[string]any)["next"].(map[string]any)
	continuation, err := decodeReadFileContinuation(next["continuation"].(string))
	if err != nil || continuation.ExpectedSHA256 != "file-hash" || continuation.Offset != 1+lineCount(m["content"].(string)) || continuation.Limit != 5000-lineCount(m["content"].(string)) {
		t.Fatalf("invalid remaining-range continuation: %+v", next)
	}
}

func TestBashProjectorExposesRankedNonOverlappingArtifactRanges(t *testing.T) {
	raw := bashEnvelope(map[string]any{
		"output":          strings.Repeat("combined\n", 5000),
		"stdout_tail":     strings.Repeat("stdout tail\n", 1000),
		"stderr_tail":     strings.Repeat("stderr tail\n", 1000),
		"full_log_sha256": "shell-hash",
		"full_log_sections": map[string]any{
			"stdout_start": 100,
			"stdout_end":   100000,
			"stderr_start": 100100,
			"stderr_end":   200000,
		},
	})
	pc := projectorContext{CallID: "bash", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/shell.log"}
	out, _, ok := projectBashResult(raw, pc)
	if !ok {
		t.Fatal("bash projector declined")
	}
	m := parseOut(t, out)
	ranges := m["continuation"].(map[string]any)["ranges"].([]any)
	if len(ranges) != 2 || ranges[0].(map[string]any)["stream"] != "stderr" || ranges[1].(map[string]any)["stream"] != "stdout" {
		t.Fatalf("bash recovery ranges are not failure-first: %+v", ranges)
	}
	for _, value := range ranges {
		next := value.(map[string]any)["next"].(map[string]any)
		continuation, err := decodeReadFileContinuation(next["continuation"].(string))
		if err != nil || continuation.ExpectedSHA256 != "shell-hash" {
			t.Fatalf("bash recovery range is not snapshot-bound: %+v", next)
		}
		if continuation.ByteOffset == nil || continuation.ByteEndOffset == nil || *continuation.ByteEndOffset <= *continuation.ByteOffset {
			t.Fatalf("empty or reversed recovery range: %+v", continuation)
		}
	}
}

func TestGenericProjectionContinuationCoversOnlyOmittedBytes(t *testing.T) {
	text := strings.Repeat("head-tail-evidence-", 5000)
	out, ok := buildBoundedResultReference("/s/generic.txt", text, false, defaultProjectionTokenBudget)
	if !ok {
		t.Fatal("first page did not fit")
	}
	m := parseOut(t, out)
	head := m["content"].(string)
	next := m["continuation"].(map[string]any)["next"].(map[string]any)
	continuation, err := decodeReadFileContinuation(next["continuation"].(string))
	if err != nil || continuation.ExpectedSHA256 == "" {
		t.Fatalf("generic continuation is not snapshot-bound: %+v", next)
	}
	if continuation.ByteOffset == nil || continuation.ByteEndOffset == nil || *continuation.ByteOffset != len(head) || *continuation.ByteEndOffset != len(text) {
		t.Fatalf("generic continuation overlaps preview: head=%d range=%+v", len(head), continuation)
	}
}

func TestGenericProjectionKeepsWholeLines(t *testing.T) {
	text := strings.Repeat("xyz\n", 10_000)
	out, ok := buildBoundedResultReference("/s/lines.txt", text, false, defaultProjectionTokenBudget)
	if !ok {
		t.Fatal("first page did not fit")
	}
	m := parseOut(t, out)
	head := m["content"].(string)
	if head == "" || !strings.HasPrefix(text, head) || !strings.HasSuffix(head, "\n") {
		t.Fatal("page did not keep a continuous prefix of whole lines")
	}
	continuation := m["continuation"].(map[string]any)
	if continuation["has_more"].(bool) != (len(head) < len(text)) {
		t.Fatalf("line-limit continuation did not match omitted bytes: %+v", continuation)
	}
}

func TestStructuredGenericProjectionHasSnapshotBoundByteContinuation(t *testing.T) {
	raw := toolresult.Result{StructuredContent: json.RawMessage(`{"records":"` + strings.Repeat("one two three", 2000) + `"}`)}
	contextual := raw.TextProjection()
	out, ok := buildBoundedResultReference("/s/structured.json", contextual, false, defaultProjectionTokenBudget)
	if !ok {
		t.Fatal("first page did not fit")
	}
	m := parseOut(t, out)
	if !strings.HasPrefix(contextual, m["content"].(string)) {
		t.Fatal("structured result did not preserve a continuous first page")
	}
	next := m["continuation"].(map[string]any)["next"].(map[string]any)
	continuation, err := decodeReadFileContinuation(next["continuation"].(string))
	if err != nil || continuation.ExpectedSHA256 == "" || continuation.ByteEndOffset == nil || *continuation.ByteEndOffset != len(contextual) {
		t.Fatalf("structured continuation is not an exact snapshot range: %+v", next)
	}
}

func TestReadFileProjectedPagesPreserveRequestedRange(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "active")
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.env.SessionDir = t.TempDir()
	var file, expected strings.Builder
	for i := 1; i <= 600; i++ {
		indent := []string{"", "\t\t", "    ", " \t "}[i%4]
		line := fmt.Sprintf("%srecord-%04d | %s", indent, i, strings.Repeat("x", 100))
		fmt.Fprintln(&file, line)
		if i >= 51 && i <= 450 {
			fmt.Fprintf(&expected, "%6d|%s\n", i, line)
		}
	}
	path := filepath.Join(root, "records.txt")
	mustWriteFile(t, path, file.String())
	tool := NewReadFileTool(kit.env)
	args := `{"path":"records.txt","offset":51,"limit":400}`
	// The portable provider schema leaves both selectors optional. The tool
	// must still reject calls that supply neither before accessing the filesystem.
	for _, invalid := range []string{`{}`, `{"path":" ","continuation":" "}`, `{"offset":51,"limit":400}`} {
		if err := tool.ValidateInput(invalid); err == nil {
			t.Fatalf("accepted missing read selector: %s", invalid)
		}
		if _, err := tool.Execute(context.Background(), invalid); err == nil {
			t.Fatalf("executed missing read selector: %s", invalid)
		}
	}
	var actual strings.Builder
	var savedNext string
	pages := 0
	for ; pages < 100; pages++ {
		if err := tool.ValidateInput(args); err != nil {
			t.Fatalf("rejected read page arguments %s: %v", args, err)
		}
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		result, _ := finalizeBuiltInToolResult(kit.env.SessionDir, "read_file", fmt.Sprintf("read-%d", pages), toolresult.FromText(raw), defaultProjectionTokenBudget)
		page := parseOut(t, result.TextProjection())
		actual.WriteString(page["content"].(string))
		if continuation, ok := page["continuation"].(map[string]any); ok && continuation["has_more"] == true {
			next, _ := json.Marshal(continuation["next"])
			args = string(next)
			savedNext = args
		} else {
			break
		}
	}
	if pages == 0 || pages == 100 || actual.String() != expected.String() {
		t.Fatalf("projected pages changed requested content: pages=%d, got bytes=%d want=%d", pages+1, actual.Len(), expected.Len())
	}
	// Copying only the content after each display delimiter must produce a
	// usable exact-edit anchor, including mixed tabs/spaces and literal pipes.
	var copied strings.Builder
	for _, numbered := range contentLines(actual.String()) {
		_, body, ok := strings.Cut(numbered, "|")
		if !ok {
			t.Fatalf("missing content boundary: %q", numbered)
		}
		copied.WriteString(body)
		copied.WriteByte('\n')
	}
	old := copied.String()
	replacement := strings.ReplaceAll(old, "record-", "updated-")
	if _, err := NewEditFileTool(kit.env).Execute(context.Background(), mustMarshalMap(map[string]any{
		"path": "records.txt", "old_text": old, "new_text": replacement,
	})); err != nil {
		t.Fatalf("exact edit from projected reads: %v", err)
	}
	if got := mustReadFile(t, path); got != strings.Replace(file.String(), old, replacement, 1) {
		t.Fatal("round-trip edit changed indentation or content outside the requested range")
	}
	mustWriteFile(t, path, "changed\n")
	if _, err := tool.Execute(context.Background(), savedNext); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("projected continuation accepted changed file: %v", err)
	}
}

func TestReadFileByteRecoverySurvivesResultSettlement(t *testing.T) {
	t.Setenv(projectionModeEnvVar, "active")
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "escaped text", body: strings.Repeat("\t\r\n\"\\<>&", 1800)},
		{name: "short lines", body: strings.Repeat("\n", 10000)},
		{name: "binary", body: strings.Repeat("\x00\xff\n", 3500)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			kit.env.SessionDir = t.TempDir()
			artifact := filepath.Join(kit.env.SessionDir, "tool-results", "output.txt")
			prefix := "previously displayed head\n"
			original := prefix + tc.body + "previously displayed tail\n"
			mustWriteFile(t, artifact, original)
			end := len(prefix) + len(tc.body)
			token := encodeReadFileByteContinuation(artifact, len(prefix), projectionPreviewBytes, end, sha256Hex([]byte(original)))
			args := mustMarshalMap(map[string]any{"continuation": token})
			var recovered strings.Builder
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber == 20 {
					t.Fatal("byte recovery did not finish")
				}
				result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
					ID: fmt.Sprintf("recover-%d", pageNumber), Name: "read_file", Arguments: args,
				})
				if err != nil {
					t.Fatal(err)
				}
				page := parseOut(t, result.TextProjection())
				if page["action"] != "read_bytes" || page["byte_offset"] != float64(len(prefix)+recovered.Len()) {
					t.Fatalf("recovery left its byte range on page %d: action=%v offset=%v", pageNumber, page["action"], page["byte_offset"])
				}
				content, _ := page["content"].(string)
				if page["encoding"] == "base64" {
					decoded, err := base64.StdEncoding.DecodeString(page["content_base64"].(string))
					if err != nil {
						t.Fatal(err)
					}
					content = string(decoded)
				}
				if len(content) == 0 || page["byte_count"] != float64(len(content)) || len(content) > projectionPreviewBytes {
					t.Fatalf("byte page count disagrees with displayed content: count=%v displayed=%d", page["byte_count"], len(content))
				}
				recovered.WriteString(content)
				continuation := page["continuation"].(map[string]any)
				if continuation["has_more"] != true {
					break
				}
				args = mustMarshalMap(continuation["next"].(map[string]any))
			}
			if recovered.String() != tc.body {
				t.Fatalf("recovery changed the omitted range: got %d bytes, want %d", recovered.Len(), len(tc.body))
			}
			mustWriteFile(t, artifact, original+"changed")
			if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{
				ID: "recover-stale", Name: "read_file", Arguments: args,
			}); err == nil || !strings.Contains(err.Error(), "stale") {
				t.Fatalf("byte recovery accepted a changed artifact: %v", err)
			}
		})
	}
}
