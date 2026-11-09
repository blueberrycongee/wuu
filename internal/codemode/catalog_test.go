package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Discovery must remain bounded, deterministic, complete through pagination,
// and separate from exact schemas and executable authority.
func TestCatalogDiscoveryContract(t *testing.T) {
	definitions := make([]ToolDefinition, 1000)
	for i := range definitions {
		definitions[i] = ToolDefinition{Name: fmt.Sprintf("mcp__docs__search_%04d", i), Description: strings.Repeat("Search documents. ", 100), InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)}
	}
	catalog, err := newToolCatalog(definitions)
	if err != nil {
		t.Fatal(err)
	}
	page, err := catalog.Search(context.Background(), "documents", 20, 0)
	if err != nil || len(page.Tools) != 20 || page.Total != 1000 || page.NextOffset == nil || *page.NextOffset != 20 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	encoded, _ := json.Marshal(page)
	if len(encoded) > 24000 || strings.Contains(string(encoded), "input_schema") {
		t.Fatalf("unbounded discovery: %d", len(encoded))
	}
	next, err := catalog.Search(context.Background(), "documents", 20, *page.NextOffset)
	if err != nil || next.Tools[0].Name == page.Tools[0].Name {
		t.Fatalf("pagination=%+v %v", next, err)
	}
	exact, err := catalog.Search(context.Background(), definitions[999].Name, 8, 0)
	if err != nil || len(exact.Tools) == 0 || exact.Tools[0].Name != definitions[999].Name {
		t.Fatalf("exact=%+v %v", exact, err)
	}
	detail, err := catalog.Describe(definitions[999].Name)
	if err != nil || string(detail.InputSchema) != string(definitions[999].InputSchema) {
		t.Fatalf("detail=%+v %v", detail, err)
	}
	definitions[999].InputSchema[0] = 'x'
	again, err := catalog.Describe(detail.Name)
	if err != nil || !json.Valid(again.InputSchema) {
		t.Fatal("catalog retained mutable schema")
	}
	detail.InputSchema[0] = 'x'
	again, _ = catalog.Describe(detail.Name)
	if !json.Valid(again.InputSchema) {
		t.Fatal("catalog returned mutable schema")
	}
	if _, err := catalog.Describe("missing"); err == nil {
		t.Fatal("missing detail accepted")
	}
	for _, args := range [][2]int{{-1, 0}, {21, 0}, {8, -1}} {
		if _, err := catalog.Search(context.Background(), "", args[0], args[1]); err == nil {
			t.Fatalf("invalid pagination accepted: %v", args)
		}
	}
}

func TestCatalogRejectsAmbiguousAndOversizedDefinitions(t *testing.T) {
	for _, definitions := range [][]ToolDefinition{
		{{Name: "same"}, {Name: "same"}}, {{Name: ""}}, {{Name: "bad", InputSchema: json.RawMessage(`{`)}},
	} {
		if _, err := newToolCatalog(definitions); err == nil {
			t.Fatalf("invalid catalog accepted: %+v", definitions)
		}
	}
	catalog, err := newToolCatalog([]ToolDefinition{{Name: "large", Description: strings.Repeat("x", 300000)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Describe("large"); err == nil {
		t.Fatal("oversized schema detail was not rejected")
	}
	page, err := catalog.Search(context.Background(), "large", 8, 0)
	if err != nil || len(page.Tools) != 1 {
		t.Fatalf("oversized detail prevented discovery: %+v %v", page, err)
	}
}

func TestCatalogSearchCancellationAndQueryBudget(t *testing.T) {
	catalog, err := newToolCatalog([]ToolDefinition{{Name: "search", Description: strings.Repeat("description ", 100000)}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := catalog.Search(ctx, "description", 8, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	words := make([]string, 17)
	for i := range words {
		words[i] = fmt.Sprintf("term%d", i)
	}
	if _, err := catalog.Search(context.Background(), strings.Join(words, " "), 8, 0); err == nil {
		t.Fatal("excessive distinct query terms accepted")
	}
	page, err := catalog.Search(context.Background(), strings.Repeat("description ", 300), 8, 0)
	if err != nil || len(page.Tools) != 1 {
		t.Fatalf("repeated terms should normalize: %+v %v", page, err)
	}
}
