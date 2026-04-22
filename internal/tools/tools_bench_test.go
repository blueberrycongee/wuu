package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// BenchmarkReadFile measures read_file tool execution.
func BenchmarkReadFile(b *testing.B) {
	env := &Env{RootDir: "/Users/blueberrycongee/wuu"}
	tool := NewReadFileTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, `{"file_path": "go.mod"}`)
	}
}

// BenchmarkGrep measures grep tool execution (spawns ripgrep subprocess).
func BenchmarkGrep(b *testing.B) {
	env := &Env{RootDir: "/Users/blueberrycongee/wuu"}
	tool := NewGrepTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, `{"pattern": "func Test", "output_mode": "files_with_matches"}`)
	}
}

// BenchmarkGlob measures glob tool execution (spawns ripgrep subprocess).
func BenchmarkGlob(b *testing.B) {
	env := &Env{RootDir: "/Users/blueberrycongee/wuu"}
	tool := NewGlobTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, `{"pattern": "*.go"}`)
	}
}

// BenchmarkPersistResultSmall measures the no-op path (result below threshold).
func BenchmarkPersistResultSmall(b *testing.B) {
	sessionDir := b.TempDir()
	call := providers.ToolCall{Name: "read_file", ID: "call_123", Arguments: `{"file_path": "go.mod"}`}
	result := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MaybePersistResult(sessionDir, call.Name, call.ID, result, defaultResultBudget)
	}
}

// BenchmarkPersistResultLarge measures the actual disk write path.
func BenchmarkPersistResultLarge(b *testing.B) {
	sessionDir := b.TempDir()
	call := providers.ToolCall{Name: "read_file", ID: "call_123", Arguments: `{"file_path": "go.mod"}`}
	// 60KB result, above the 50KB threshold
	result := make([]byte, 60_000)
	for i := range result {
		result[i] = 'x'
	}
	resultStr := string(result)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = MaybePersistResult(sessionDir, call.Name, call.ID+string(rune(i)), resultStr, defaultResultBudget)
	}
}

// BenchmarkEditFile measures edit_file tool execution.
func BenchmarkEditFile(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello world\nline two\nline three\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		b.Fatal(err)
	}
	env := &Env{RootDir: dir}
	tool := NewEditFileTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = os.WriteFile(path, content, 0644)
		_, _ = tool.Execute(ctx, `{"file_path": "test.txt", "old_string": "line two", "new_string": "line 2"}`)
	}
}

// BenchmarkWriteFile measures write_file tool execution.
func BenchmarkWriteFile(b *testing.B) {
	dir := b.TempDir()
	env := &Env{RootDir: dir}
	tool := NewWriteFileTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, `{"file_path": "test.go", "content": "package main\n\nfunc main() {}\n"}`)
	}
}
