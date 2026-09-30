package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxCatalogDetailBytes = 256 * 1024

type toolCatalog struct {
	definitions map[string]ToolDefinition
	names       []string
}
type catalogSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
type catalogSearchResult struct {
	Tools      []catalogSummary `json:"tools"`
	Total      int              `json:"total"`
	NextOffset *int             `json:"next_offset,omitempty"`
}

func newToolCatalog(definitions []ToolDefinition) (*toolCatalog, error) {
	if len(definitions) > 10000 {
		return nil, errors.New("PTC catalog exceeds 10000 tools")
	}
	catalog := &toolCatalog{definitions: make(map[string]ToolDefinition, len(definitions))}
	total := 0
	for _, definition := range definitions {
		if definition.Name == "" || len(definition.Name) > 256 || !utf8.ValidString(definition.Name) {
			return nil, errors.New("invalid PTC tool name")
		}
		if _, exists := catalog.definitions[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate PTC tool name %q", definition.Name)
		}
		if len(definition.InputSchema) == 0 {
			definition.InputSchema = json.RawMessage(`{}`)
		}
		total += len(definition.Name) + len(definition.Description) + len(definition.InputSchema)
		if total > 32*1024*1024 {
			return nil, errors.New("PTC catalog exceeds 32 MiB")
		}
		if !json.Valid(definition.InputSchema) {
			return nil, fmt.Errorf("invalid input schema for %q", definition.Name)
		}
		definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
		catalog.definitions[definition.Name] = definition
		catalog.names = append(catalog.names, definition.Name)
	}
	sort.Strings(catalog.names)
	return catalog, nil
}
func (c *toolCatalog) Names() []string { return append([]string(nil), c.names...) }

func (c *toolCatalog) Describe(name string) (ToolDefinition, error) {
	definition, ok := c.definitions[name]
	if !ok {
		return ToolDefinition{}, fmt.Errorf("tool %q is unavailable", name)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return ToolDefinition{}, err
	}
	if len(encoded) > maxCatalogDetailBytes {
		return ToolDefinition{}, fmt.Errorf("tool %q description and schema exceed the 256 KiB detail limit", name)
	}
	definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	return definition, nil
}

func (c *toolCatalog) Search(ctx context.Context, query string, limit, offset int) (catalogSearchResult, error) {
	result := catalogSearchResult{Tools: []catalogSummary{}}
	if limit == 0 {
		limit = 8
	}
	if limit < 1 || limit > 20 || offset < 0 || len(query) > 4096 {
		return result, errors.New("searchTools requires limit 1..20, non-negative offset, and a query of at most 4096 bytes")
	}
	query = strings.ToLower(strings.TrimSpace(query))
	tokens := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	seen := make(map[string]bool)
	unique := tokens[:0]
	for _, token := range tokens {
		if !seen[token] {
			seen[token] = true
			unique = append(unique, token)
		}
	}
	tokens = unique
	if len(tokens) > 16 {
		return result, errors.New("searchTools supports at most 16 distinct query terms")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	type match struct {
		name  string
		score int
	}
	matches := make([]match, 0)
	for _, name := range c.names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		definition := c.definitions[name]
		lowerName := strings.ToLower(name)
		description := strings.ToLower(definition.Description)
		score := 0
		if query == "" {
			score = 1
		}
		if query == lowerName {
			score += 10000
		}
		for _, token := range tokens {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if strings.Contains(lowerName, token) {
				score += 10
			}
			if strings.Contains(description, token) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, match{name, score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].name < matches[j].name
	})
	result.Total = len(matches)
	if offset >= len(matches) {
		return result, nil
	}
	end := min(offset+limit, len(matches))
	for _, match := range matches[offset:end] {
		description := []rune(c.definitions[match.name].Description)
		if len(description) > 240 {
			description = append(description[:239], '…')
		}
		result.Tools = append(result.Tools, catalogSummary{Name: match.name, Description: string(description)})
	}
	if end < len(matches) {
		result.NextOffset = &end
	}
	return result, nil
}
