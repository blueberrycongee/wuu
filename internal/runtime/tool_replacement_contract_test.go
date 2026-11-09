package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const validSearch = `{"action":"web_search","query":"hello","evidence":{"id":"web_123","kind":"search","source":"https://example.test","source_tier":"search_index","retrieved_at":"2026-10-29T00:00:00Z","version_matched_to_repo":"unknown"},"results":[]}`

func TestReplacementSearchResultContract(t *testing.T) {
	for _, text := range []string{`{}`, `{"action":"web_search","query":"hello","evidence":{"id":"bad\nline"},"results":[]}`, `{"action":"web_search","query":"other","evidence":{"id":"x"},"results":[]}`} {
		if _, err := validateSearchReplacement(toolresult.FromText(text), `{"query":"hello"}`); err == nil {
			t.Fatalf("accepted invalid result: %s", text)
		}
	}
	if _, err := validateSearchReplacement(toolresult.FromText(validSearch), `{"query":"hello"}`); err != nil {
		t.Fatal(err)
	}
}
func TestReplacementSearchResultStructuredContent(t *testing.T) {
	structured := toolresult.Result{StructuredContent: json.RawMessage(validSearch)}
	if _, err := validateSearchReplacement(structured, `{"query":"hello"}`); err != nil {
		t.Fatal(err)
	}
	both := toolresult.FromText(validSearch)
	both.StructuredContent = json.RawMessage(validSearch)
	if _, err := validateSearchReplacement(both, `{"query":"hello"}`); err != nil {
		t.Fatal(err)
	}
	both.StructuredContent = json.RawMessage(`{"action":"wrong"}`)
	if _, err := validateSearchReplacement(both, `{"query":"hello"}`); err == nil {
		t.Fatal("contradictory result accepted")
	}
	malformed := toolresult.FromText(validSearch)
	malformed.Content = append(malformed.Content, toolresult.ContentPart{Type: toolresult.ContentTypeText, Text: "extra"})
	if _, err := validateSearchReplacement(malformed, `{"query":"hello"}`); err == nil {
		t.Fatal("multiple content parts accepted")
	}
}

func TestReplacementSearchExtraFieldsPreserveEvidence(t *testing.T) {
	for _, outcome := range []struct {
		name, payload, want string
	}{
		{"results", `"results":[{"url":"https://example.test/result"}]`, "result_count=1"},
		{"error", `"error":"search unavailable"`, "error=search unavailable"},
	} {
		text := strings.Replace(validSearch, `"results":[]`, outcome.payload+`,"status_code":"200","content_type":{},"size":"unknown","truncated":"unknown"`, 1)
		for _, view := range []string{"text", "structured", "both"} {
			t.Run(outcome.name+"/"+view, func(t *testing.T) {
				result := toolresult.Result{}
				if view != "structured" {
					result = toolresult.FromText(text)
				}
				if view != "text" {
					result.StructuredContent = json.RawMessage(text)
				}
				evidence, err := validateSearchReplacement(result, `{"query":"hello"}`)
				if err != nil {
					t.Fatal(err)
				}
				kit, err := tools.New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				kit.RecordWebSearchEvidence(evidence)
				block, ok := kit.WebEvidenceContextBlock()
				if !ok || !strings.Contains(block.Content, "id=web_123 tool=web_search") || !strings.Contains(block.Content, outcome.want) {
					t.Fatalf("accepted replacement result lost evidence: %+v", block)
				}
			})
		}
	}
}

func TestReplacementSearchRejectsMalformedEntries(t *testing.T) {
	for _, replacement := range []string{`"results":[1]`, `"results":[{"url":""}]`, `"results":[],"error":"failed"`} {
		text := strings.Replace(validSearch, `"results":[]`, replacement, 1)
		if _, err := validateSearchReplacement(toolresult.FromText(text), `{"query":"hello"}`); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
}

func TestReplacementSearchEvidenceVersionMatchesRequest(t *testing.T) {
	input := `{"query":"hello","version_hint":"example@1.2"}`
	result := toolresult.FromText(validSearch)
	if _, err := validateSearchReplacement(result, input); err == nil {
		t.Fatal("accepted incorrect version")
	}
	text := strings.Replace(validSearch, `"version_matched_to_repo":"unknown"`, `"version_matched_to_repo":"example@1.2"`, 1)
	if _, err := validateSearchReplacement(toolresult.FromText(text), input); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementRemovalClearsClonedBindings(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetReplacementBindings(map[string]config.ToolReplacement{"web_search": {PluginID: "old", ToolID: "search"}})
	clone, err := kit.CloneForRoot(kit.RootDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := newPluginToolExecutor(clone, pluginhost.New(), "thread", clone.RootDir())
	if err := configureToolReplacements(executor, clone, nil); err != nil {
		t.Fatal(err)
	}
	if len(clone.ReplacementBindings()) != 0 {
		t.Fatal("old bindings persisted after removal")
	}
	if !clone.SupportsTool("web_search") {
		t.Fatal("native tool not restored after explicit binding removal")
	}
}
