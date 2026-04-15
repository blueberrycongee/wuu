package insight

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const maxConcurrentLLM = 3

// GenerateInsights runs parallel LLM calls to produce insight sections,
// then synthesizes the "At a Glance" summary.
func GenerateInsights(ctx context.Context, client providers.Client, model string, agg AggregatedData, facets map[string]Facet, progress chan<- ProgressEvent) ([]InsightSection, AtAGlance, error) {
	dataContext := buildDataContext(agg, facets)

	// Generate sections in parallel with bounded concurrency.
	type result struct {
		idx     int
		section InsightSection
		err     error
	}
	results := make([]result, len(insightSections))
	sem := make(chan struct{}, maxConcurrentLLM)
	var wg sync.WaitGroup
	var completed int32
	var mu sync.Mutex

	for i, def := range insightSections {
		wg.Add(1)
		go func(i int, def insightSectionDef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			prompt := fmt.Sprintf(def.Prompt, dataContext)
			section, err := generateSection(ctx, client, model, def, prompt)
			results[i] = result{idx: i, section: section, err: err}

			mu.Lock()
			completed++
			n := completed
			mu.Unlock()

			status := "done"
			if err != nil {
				status = "failed"
			}
			pct := 0.75 + 0.15*float64(n)/float64(len(insightSections))
			if progress != nil {
				progress <- ProgressEvent{
					Phase:  "generating",
					Detail: fmt.Sprintf("Section %d/%d \"%s\" %s", n, len(insightSections), def.Title, status),
					Pct:    pct,
				}
			}
		}(i, def)
	}
	wg.Wait()

	// Collect successful sections.
	var sections []InsightSection
	for _, r := range results {
		if r.err != nil {
			// Include error note but don't fail the whole report.
			sections = append(sections, InsightSection{
				Name:    insightSections[r.idx].Name,
				Title:   insightSections[r.idx].Title,
				Content: fmt.Sprintf("_Unable to generate this section: %v_", r.err),
			})
			continue
		}
		sections = append(sections, r.section)
	}

	// Synthesize "At a Glance" from all sections (sequential, needs section outputs).
	glance, err := synthesizeAtAGlance(ctx, client, model, sections, agg)
	if err != nil {
		// Non-fatal: provide a basic summary.
		glance = AtAGlance{
			WhatsWorking:       "Unable to generate summary.",
			WhatsHindering:     "",
			QuickWins:          "",
			AmbitiousWorkflows: "",
		}
	}

	return sections, glance, nil
}

func generateSection(ctx context.Context, client providers.Client, model string, def insightSectionDef, prompt string) (InsightSection, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	resp, err := client.Chat(reqCtx, providers.ChatRequest{
		Model: model,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
	})
	if err != nil {
		return InsightSection{}, err
	}
	return InsightSection{
		Name:    def.Name,
		Title:   def.Title,
		Content: strings.TrimSpace(resp.Content),
	}, nil
}

func synthesizeAtAGlance(ctx context.Context, client providers.Client, model string, sections []InsightSection, agg AggregatedData) (AtAGlance, error) {
	// Build context from all sections.
	var b strings.Builder
	fmt.Fprintf(&b, "Sessions: %d, Messages: %d, Duration: %.1f hours, Days active: %d\n\n",
		agg.TotalSessions, agg.TotalMessages, agg.TotalDurationH, agg.DaysActive)
	for _, s := range sections {
		fmt.Fprintf(&b, "## %s\n%s\n\n", s.Title, s.Content)
	}

	prompt := fmt.Sprintf(atAGlancePrompt, b.String())
	reqCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	resp, err := client.Chat(reqCtx, providers.ChatRequest{
		Model: model,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return AtAGlance{}, err
	}

	return parseAtAGlanceResponse(resp.Content)
}

func parseAtAGlanceResponse(content string) (AtAGlance, error) {
	content = strings.TrimSpace(content)
	var glance AtAGlance

	// Try direct parse.
	if err := json.Unmarshal([]byte(content), &glance); err == nil {
		return glance, nil
	}

	// Try extracting from code fence.
	if extracted := extractJSON(content); extracted != "" {
		if err := json.Unmarshal([]byte(extracted), &glance); err == nil {
			return glance, nil
		}
	}

	// Fallback: extract the first syntactically valid JSON object.
	if candidate := extractFirstJSONObject(content); candidate != "" {
		if err := json.Unmarshal([]byte(candidate), &glance); err == nil {
			return glance, nil
		}
	}

	return AtAGlance{WhatsWorking: truncateStr(content, 300)}, nil
}

// buildDataContext formats aggregated data and facets as text for LLM prompts.
func buildDataContext(agg AggregatedData, facets map[string]Facet) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Overview\n")
	fmt.Fprintf(&b, "- Total sessions: %d\n", agg.TotalSessions)
	fmt.Fprintf(&b, "- Total messages: %d\n", agg.TotalMessages)
	fmt.Fprintf(&b, "- Total duration: %.1f hours\n", agg.TotalDurationH)
	fmt.Fprintf(&b, "- Days active: %d\n", agg.DaysActive)
	fmt.Fprintf(&b, "- Messages per day: %.1f\n", agg.MessagesPerDay)
	fmt.Fprintf(&b, "- Date range: %s to %s\n", agg.DateRange[0], agg.DateRange[1])
	fmt.Fprintf(&b, "- Est. tokens: %d\n\n", agg.TotalEstTokens)

	if len(agg.ToolCounts) > 0 {
		fmt.Fprintf(&b, "## Tool Usage (top 15)\n")
		for _, kv := range topN(agg.ToolCounts, 15) {
			fmt.Fprintf(&b, "- %s: %d\n", kv.key, kv.val)
		}
		b.WriteString("\n")
	}

	if len(agg.Languages) > 0 {
		fmt.Fprintf(&b, "## Languages\n")
		for _, kv := range topN(agg.Languages, 10) {
			fmt.Fprintf(&b, "- %s: %d\n", kv.key, kv.val)
		}
		b.WriteString("\n")
	}

	if len(agg.GoalCategories) > 0 {
		fmt.Fprintf(&b, "## Goal Categories\n")
		for _, kv := range topN(agg.GoalCategories, 10) {
			fmt.Fprintf(&b, "- %s: %d\n", kv.key, kv.val)
		}
		b.WriteString("\n")
	}

	if len(agg.Outcomes) > 0 {
		fmt.Fprintf(&b, "## Outcomes\n")
		for k, v := range agg.Outcomes {
			fmt.Fprintf(&b, "- %s: %d\n", k, v)
		}
		b.WriteString("\n")
	}

	if len(agg.Friction) > 0 {
		fmt.Fprintf(&b, "## Friction Types\n")
		for _, kv := range topN(agg.Friction, 10) {
			fmt.Fprintf(&b, "- %s: %d\n", kv.key, kv.val)
		}
		b.WriteString("\n")
	}

	if len(agg.SessionTypes) > 0 {
		fmt.Fprintf(&b, "## Session Types\n")
		for k, v := range agg.SessionTypes {
			fmt.Fprintf(&b, "- %s: %d\n", k, v)
		}
		b.WriteString("\n")
	}

	// Include session summaries.
	if len(agg.Summaries) > 0 {
		fmt.Fprintf(&b, "## Recent Session Summaries (up to 30)\n")
		limit := 30
		if len(agg.Summaries) < limit {
			limit = len(agg.Summaries)
		}
		for _, s := range agg.Summaries[:limit] {
			goal := s.Goal
			if goal == "" {
				goal = s.Summary
			}
			fmt.Fprintf(&b, "- [%s] %s\n", s.Date, truncateStr(goal, 100))
		}
		b.WriteString("\n")
	}

	// Friction details from facets.
	var frictionDetails []string
	for _, f := range facets {
		if f.FrictionDetail != "" {
			frictionDetails = append(frictionDetails, f.FrictionDetail)
		}
	}
	if len(frictionDetails) > 0 {
		fmt.Fprintf(&b, "## Friction Details (up to 20)\n")
		limit := 20
		if len(frictionDetails) < limit {
			limit = len(frictionDetails)
		}
		for _, d := range frictionDetails[:limit] {
			fmt.Fprintf(&b, "- %s\n", d)
		}
		b.WriteString("\n")
	}

	return b.String()
}

type kvPair struct {
	key string
	val int
}

func topN(m map[string]int, n int) []kvPair {
	pairs := make([]kvPair, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kvPair{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].val > pairs[j].val
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return pairs
}
