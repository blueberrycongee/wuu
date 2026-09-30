package tools

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/mcp"
)

// mcpSnapshotLen reads the current MCP catalog snapshot size.
func mcpSnapshotLen(t *Toolkit) int {
	t.mcpCatalogMu.RLock()
	defer t.mcpCatalogMu.RUnlock()
	return len(t.mcpTools)
}

func TestFreezeToolSurfacePinsMCPCatalogUntilUnfreeze(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a connected server whose catalog changed asynchronously: the
	// cached snapshot generation differs from the manager's current one.
	kit.mcpManager = mcp.NewManager()
	kit.FreezeToolSurface()
	sentinel := &mcp.MCPTool{}
	kit.mcpCatalogMu.Lock()
	kit.mcpCatalogGeneration = 5 // manager reports 0 → stale
	kit.mcpTools = []*mcp.MCPTool{sentinel}
	kit.mcpCatalogMu.Unlock()

	kit.refreshMCPToolSnapshot(false)
	if got := mcpSnapshotLen(kit); got != 1 {
		t.Fatalf("frozen surface must keep the pinned MCP snapshot, got %d tools", got)
	}

	// Nested freeze: inner unfreeze must not release the outer pin.
	kit.FreezeToolSurface()
	kit.UnfreezeToolSurface()
	kit.refreshMCPToolSnapshot(false)
	if got := mcpSnapshotLen(kit); got != 1 {
		t.Fatalf("nested freeze must keep the pin, got %d tools", got)
	}

	kit.UnfreezeToolSurface()
	kit.refreshMCPToolSnapshot(false)
	if got := mcpSnapshotLen(kit); got != 0 {
		t.Fatalf("after unfreeze the deferred catalog change must land, got %d tools", got)
	}
}

func TestFreezeRefreshesPendingCatalogBeforeNextRun(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.mcpManager = mcp.NewManager()
	kit.mcpCatalogGeneration = 5
	kit.mcpTools = []*mcp.MCPTool{{}}
	kit.FreezeToolSurface()
	defer kit.UnfreezeToolSurface()
	if got := mcpSnapshotLen(kit); got != 0 {
		t.Fatalf("new run retained stale catalog: %d", got)
	}
}
