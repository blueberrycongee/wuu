package composition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The cloud example is an explicit provider selection. Resolution must not
// mutate the shipped desktop graph or silently substitute its default loop.
func TestDurableCompositionIsolation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "compositions", "durable", "composition.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	durable, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if durable.Includes("wuu.default-loop") || !durable.Includes("example.durable-driver") || !durable.Includes("wuu.durable-wait") {
		t.Fatalf("durable graph did not select the independent cloud implementation: %+v", durable)
	}
	for _, module := range []string{"wuu.state.sqlite", "wuu.session-execution", "wuu.tool-execution", "wuu.model.native", "wuu.permissions.default", "wuu.prompt.default", "wuu.compaction.default", "wuu.agent.policy.default"} {
		if !durable.Includes(module) {
			t.Fatalf("cloud graph omitted %s", module)
		}
	}
	found := false
	for _, binding := range durable.Services {
		if binding.Service == "driver.default" && binding.Major == 2 && binding.Provider == "example.durable-driver" {
			found = true
		}
	}
	if !found {
		t.Fatal("cloud graph has no explicit public driver.default@2 binding")
	}
	for i := 0; i < 2; i++ {
		desktop, err := DefaultConfig("desktop").Resolve()
		if err != nil {
			t.Fatal(err)
		}
		if !desktop.Includes("wuu.default-loop") || desktop.Includes("example.durable-driver") || desktop.Includes("wuu.durable-wait") || desktop.Includes("wuu.durable-client") {
			t.Fatalf("cloud selection contaminated desktop default: %+v", desktop)
		}
	}
}
