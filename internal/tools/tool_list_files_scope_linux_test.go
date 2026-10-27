//go:build linux

package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"golang.org/x/sys/unix"
)

// A directory listing must not hydrate file contents or recursively open an
// unrelated directory, including in its telemetry and run_code wrappers.
func TestListFilesDoesNotOpenWorkspaceContents(t *testing.T) {
	for _, route := range []string{"direct", "toolkit", "toolkit_prior_read", "summary_only", "run_code", "run_code_absolute"} {
		t.Run(route, func(t *testing.T) {
			kit := newCodeModeTestToolkit(t)
			root := kit.RootDir()
			t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
			listingPath, listingDir := "docs", filepath.Join(root, "docs")
			if route == "run_code_absolute" {
				listingDir = t.TempDir()
				listingPath = listingDir
			}
			args := mustMarshalMap(map[string]any{"path": listingPath})
			selected := filepath.Join(listingDir, "visible.txt")
			unrelated := filepath.Join(root, "unrelated", "nested", "content.txt")
			mustWriteFile(t, selected, "directory metadata only")
			mustWriteFile(t, unrelated, "must not be opened")
			if route == "toolkit_prior_read" || route == "summary_only" {
				_, err := kit.ExecuteResult(toolctx.WithNestedCall(context.Background()), providers.ToolCall{Name: "read_file", Arguments: `{"path":"docs/visible.txt"}`})
				if err != nil {
					t.Fatal(err)
				}
			}
			fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			for _, path := range []string{selected, unrelated, filepath.Join(root, "unrelated")} {
				if _, err := unix.InotifyAddWatch(fd, path, unix.IN_OPEN|unix.IN_ACCESS); err != nil {
					t.Fatal(err)
				}
			}
			switch route {
			case "summary_only":
				kit.ToolResultSummaryContextBlock()
			case "direct":
				_, err = NewListFilesTool(kit.env).Execute(context.Background(), args)
			case "toolkit", "toolkit_prior_read":
				_, err = kit.ExecuteResult(toolctx.WithNestedCall(context.Background()), providers.ToolCall{Name: "list_files", Arguments: args})
				kit.ToolResultSummaryContextBlock()
			case "run_code", "run_code_absolute":
				result := runPTCProgram(t, kit, fmt.Sprintf(`const results = await Promise.all([tools.list_files(%s), tools.list_files(%s)]); text(results);`, args, args))
				if result.IsError {
					t.Fatal(result.TextProjection())
				}
				kit.ToolResultSummaryContextBlock()
			}
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 4096)
			if n, err := unix.Read(fd, buf); n > 0 || !errors.Is(err, unix.EAGAIN) {
				t.Fatalf("listing accessed file content or an unrelated subtree: events=%d err=%v", n, err)
			}
		})
	}
}
