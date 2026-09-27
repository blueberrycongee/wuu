package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGrepFallbackLongLinesAndPagination(t *testing.T) {
	root := t.TempDir()
	// An irrelevant generated line must not hide later matches. A match beyond
	// the display limit must still count, even when it occurs twice on one line.
	long := strings.Repeat("x", 128*1024)
	content := long + "\n" + long + " needle needle\r\n" + strings.Repeat("needle\n", grepPageSize)
	mustWriteFile(t, filepath.Join(root, "generated.txt"), content)
	kit := searchFixtureKit(t, root, t.TempDir())
	t.Setenv("PATH", t.TempDir()) // Exercise the actual no-ripgrep dispatch.
	page := parseSearchFixturePage(t, searchFixtureCall(t, kit, "grep", map[string]any{"pattern": "needle"}))
	if page.Total != grepPageSize+1 || len(page.Matches) == 0 {
		t.Fatalf("unexpected long-line page: total=%d matches=%d", page.Total, len(page.Matches))
	}
	if page.Matches[0].Line != 2 || !page.Matches[0].ContentTruncated {
		t.Fatalf("missing long-line match: %+v", page.Matches[0])
	}
	seen := 0
	for {
		for _, match := range page.Matches {
			if match.Line != seen+2 {
				t.Fatalf("lost or duplicated line after %d matches: %+v", seen, match)
			}
			seen++
		}
		if page.Page.Next.Offset == 0 {
			break
		}
		if page.Page.Next.Offset != seen {
			t.Fatalf("non-progressing continuation: offset=%d seen=%d", page.Page.Next.Offset, seen)
		}
		page = parseSearchFixturePage(t, searchFixtureCall(t, kit, "grep", map[string]any{
			"pattern": "needle", "offset": page.Page.Next.Offset, "expected_revision": page.Page.Next.Revision,
		}))
	}
	if seen != grepPageSize+1 {
		t.Fatalf("incomplete pagination: %d matches", seen)
	}
	counts := parseSearchFixturePage(t, searchFixtureCall(t, kit, "grep", map[string]any{"pattern": "needle", "output_mode": "count"}))
	if len(counts.Counts) != 1 || counts.Counts[0].Count != grepPageSize+1 {
		t.Fatalf("count must count matching lines, like rg --count: %+v", counts.Counts)
	}
	files := parseSearchFixturePage(t, searchFixtureCall(t, kit, "grep", map[string]any{"pattern": "^needle$", "output_mode": "files_with_matches"}))
	if !reflect.DeepEqual(files.Files, []string{"generated.txt"}) {
		t.Fatalf("anchored pattern must match individual lines: %+v", files.Files)
	}
}

func TestGrepFallbackGitIgnoreAcrossModes(t *testing.T) {
	root := searchFixtureRepo(t)
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "thirdparty/\n*.generated\n!keep.generated\n")
	mustWriteFile(t, filepath.Join(root, "thirdparty", "large.txt"), strings.Repeat("x", 128*1024)+"needle\n")
	mustWriteFile(t, filepath.Join(root, "drop.generated"), "needle\n")
	mustWriteFile(t, filepath.Join(root, "keep.generated"), "needle\n")
	mustWriteFile(t, filepath.Join(root, "sub", ".gitignore"), "hidden.txt\n")
	mustWriteFile(t, filepath.Join(root, "sub", "hidden.txt"), "needle\n")
	mustWriteFile(t, filepath.Join(root, "sub", "visible.txt"), "needle\n")
	// Even tracked files are excluded by ripgrep when an ignore rule matches.
	searchFixtureGit(t, root, "add", "-f", "drop.generated")
	want := []string{"keep.generated", "sub/visible.txt"}
	ctx := context.Background()
	matches, err := grepWithFallback(ctx, nil, root, "needle", root, "", grepOptions{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, match := range matches {
		got = append(got, match.File)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content files = %v, want %v", got, want)
	}
	files, err := grepFilesWithMatchesFallback(ctx, root, "needle", root, "", grepOptions{}, 0)
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, err = %v", files, err)
	}
	counts, total, err := grepCountMatchesFallback(ctx, root, "needle", root, "", grepOptions{}, 0)
	if err != nil || total != 2 || len(counts) != 2 || counts[0].File != want[0] || counts[1].File != want[1] {
		t.Fatalf("counts = %v, total = %d, err = %v", counts, total, err)
	}
	explicit, err := grepWithFallback(ctx, nil, root, "needle", filepath.Join(root, "drop.generated"), "", grepOptions{}, 0)
	if err != nil || len(explicit) != 1 {
		t.Fatalf("explicit ignored file = %v, err = %v", explicit, err)
	}
	// A scoped directory still uses parent and nested ignore rules.
	files, err = grepFilesWithMatchesFallback(ctx, root, "needle", filepath.Join(root, "sub"), "", grepOptions{}, 0)
	if err != nil || !reflect.DeepEqual(files, []string{"sub/visible.txt"}) {
		t.Fatalf("scoped files = %v, err = %v", files, err)
	}
}
