package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// BenchmarkReadFile measures read_file tool execution.
func BenchmarkReadFile(b *testing.B) {
	env := &Env{RootDir: benchmarkRootDir(b)}
	tool := NewReadFileTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, `{"file_path": "go.mod"}`)
	}
}

// BenchmarkGrep measures grep tool execution (spawns ripgrep subprocess).
func BenchmarkGrep(b *testing.B) {
	env := &Env{RootDir: benchmarkRootDir(b)}
	tool := NewGrepTool(env)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Execute(ctx, `{"pattern": "func Test", "output_mode": "files_with_matches"}`)
	}
}

// BenchmarkGlob measures glob tool execution (spawns ripgrep subprocess).
func BenchmarkGlob(b *testing.B) {
	env := &Env{RootDir: benchmarkRootDir(b)}
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
	result := toolresult.FromText("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = finalizeGenericToolResult(sessionDir, "call_123", result, defaultProjectionTokenBudget)
	}
}

// BenchmarkPersistResultLarge measures the actual disk write path.
func BenchmarkPersistResultLarge(b *testing.B) {
	sessionDir := b.TempDir()
	// 60KB result, above the 50KB threshold
	result := make([]byte, 60_000)
	for i := range result {
		result[i] = 'x'
	}
	resultValue := toolresult.FromText(string(result))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = finalizeGenericToolResult(sessionDir, "call_"+strconv.Itoa(i), resultValue, defaultProjectionTokenBudget)
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

func benchmarkRootDir(b *testing.B) string {
	b.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDir, "..", ".."))
}
