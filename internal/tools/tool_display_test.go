package tools

import (
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestToolkitToolDisplayFormatsBuiltInTools(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name string
		call providers.ToolCall
		want providers.ToolCallDisplay
	}{
		{
			name: "list root",
			call: providers.ToolCall{Name: "list_files", Arguments: `{"path":"."}`},
			want: providers.ToolCallDisplay{Kind: "read", Text: "查看项目目录"},
		},
		{
			name: "read file",
			call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"internal/appserver/model.go"}`},
			want: providers.ToolCallDisplay{Kind: "read", Text: "读取 model.go"},
		},
		{
			name: "glob",
			call: providers.ToolCall{Name: "glob", Arguments: `{"pattern":"**/AGENTS.md","path":"."}`},
			want: providers.ToolCallDisplay{Kind: "search", Text: "搜索 AGENTS.md"},
		},
		{
			name: "git status",
			call: providers.ToolCall{Name: "git", Arguments: `{"subcommand":"status","args":[]}`},
			want: providers.ToolCallDisplay{Kind: "command", Text: "检查 Git 状态"},
		},
		{
			name: "bash verification",
			call: providers.ToolCall{Name: "bash", Arguments: `{"command":"go test ./..."}`},
			want: providers.ToolCallDisplay{Kind: "test", Text: "验证 go test ./...", Capability: "command.bash"},
		},
		{
			name: "bash background",
			call: providers.ToolCall{Name: "bash", Arguments: `{"action":"start_background","command":"npm run dev"}`},
			want: providers.ToolCallDisplay{Kind: "command", Text: "启动 npm run dev", Capability: "command.background"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := kit.ToolDisplay(tt.call)
			if !ok {
				t.Fatal("expected display metadata")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unexpected display: got %+v want %+v", got, tt.want)
			}
		})
	}
}

func TestToolkitToolDisplayLeavesMCPRaw(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got, ok := kit.ToolDisplay(providers.ToolCall{Name: "mcp_docs_search", Arguments: `{"query":"abc"}`}); ok {
		t.Fatalf("MCP tools should not get built-in display metadata, got %+v", got)
	}
}

func TestToolkitToolDisplayAddsCapabilityForActiveSurface(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", true)

	got, ok := kit.ToolDisplay(providers.ToolCall{Name: "bash", Arguments: `{"command":"npm test"}`})
	if !ok {
		t.Fatal("expected display metadata")
	}
	if got.Capability != "command.bash" {
		t.Fatalf("Capability = %q, want command.bash; display=%+v", got.Capability, got)
	}

	got, ok = kit.ToolDisplay(providers.ToolCall{Name: "bash", Arguments: `{"action":"start_background","command":"npm run dev"}`})
	if !ok {
		t.Fatal("expected display metadata")
	}
	if got.Capability != "command.background" {
		t.Fatalf("Capability = %q, want command.background; display=%+v", got.Capability, got)
	}

	got, ok = kit.ToolDisplay(providers.ToolCall{Name: "apply_patch", Arguments: `{}`})
	if !ok {
		t.Fatal("expected display metadata")
	}
	if got.Capability != "file.edit" {
		t.Fatalf("Capability = %q, want file.edit; display=%+v", got.Capability, got)
	}
}
