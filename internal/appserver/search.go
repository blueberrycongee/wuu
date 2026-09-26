package appserver

import (
	"database/sql"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	defaultThreadSearchLimit     = 100
	maxThreadSearchLimit         = 100
	threadSearchSnippetRunes     = 180
	threadSearchTechnicalRank    = 1
	threadSearchConversationRank = 2
	threadSearchTitleRank        = 3
)

type threadSearchSource struct {
	entry threadListEntry
	live  *threadState
}

type threadSearchCandidate struct {
	text string
	rank int
}

func (s *Server) handleThreadSearch(req Request) error {
	var params ThreadSearchParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	query := normalizeThreadSearchText(params.Query)
	limit := params.Limit
	if limit <= 0 {
		limit = defaultThreadSearchLimit
	}
	if limit > maxThreadSearchLimit {
		limit = maxThreadSearchLimit
	}

	sources, err := s.threadSearchSources()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	results := make([]threadSearchResultEntry, 0, min(len(sources), limit))
	// Resolve every possible title match before opening transcripts. Sources
	// already use the tie-break order, so later titles cannot displace a full page.
	titleMatches := make(map[string]bool)
	if query != "" {
		for _, source := range sources {
			title := source.entry.thread.Title
			if strings.TrimSpace(title) == "" {
				title = source.entry.thread.Preview
			}
			if !strings.Contains(normalizeThreadSearchText(title), query) {
				continue
			}
			titleMatches[source.entry.thread.ID] = true
			results = append(results, threadSearchResultEntry{
				entry:  source.entry,
				result: ThreadSearchResultItem{Thread: source.entry.thread, Snippet: threadSearchExcerpt(title, query)},
				rank:   threadSearchTitleRank,
			})
			if len(results) >= limit {
				break
			}
		}
	}
	var searchDB *sql.DB
	defer func() {
		if searchDB != nil {
			searchDB.Close()
		}
	}()
	strongMatches := len(results)
	for _, source := range sources {
		// All titles are accounted for. Once a page of conversation-or-better
		// matches exists, remaining sources can only lose the tie-break.
		if strongMatches >= limit {
			break
		}
		if titleMatches[source.entry.thread.ID] {
			continue
		}
		// Resolve metadata first. Only a technical match or a miss needs
		// history: it may contain a higher-ranked conversation match.
		candidates := threadSearchCandidates(source.entry.thread, nil)
		snippet := ""
		rank := 0
		if query != "" {
			snippet, rank = threadSearchMatchSnippet(candidates, query)
		} else {
			snippet = threadSearchDefaultSnippet(candidates)
		}
		if snippet == "" || (query != "" && rank < threadSearchConversationRank) {
			if source.live == nil && searchDB == nil {
				searchDB, err = session.OpenStore(s.rt.SessionDir)
				if err != nil {
					return s.writeResponse(req.ID, nil, err)
				}
			}
			history, err := s.threadSearchHistory(source, searchDB)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			candidates = threadSearchCandidates(source.entry.thread, history)
			if query != "" {
				snippet, rank = threadSearchMatchSnippet(candidates, query)
			} else {
				snippet = threadSearchDefaultSnippet(candidates)
			}
		}
		if query != "" && snippet == "" {
			continue
		}
		results = append(results, threadSearchResultEntry{
			entry:  source.entry,
			result: ThreadSearchResultItem{Thread: source.entry.thread, Snippet: snippet},
			rank:   rank,
		})
		if rank >= threadSearchConversationRank {
			strongMatches++
		}
		if query == "" && len(results) >= limit {
			break
		}
	}
	sortThreadSearchResults(results)
	results = results[:min(len(results), limit)]
	out := make([]ThreadSearchResultItem, 0, len(results))
	for _, result := range results {
		out = append(out, result.result)
	}
	return s.writeResponse(req.ID, ThreadSearchResult{Results: out}, nil)
}

func (s *Server) threadSearchSources() ([]threadSearchSource, error) {
	sessions, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return nil, err
	}
	sourcesByID := make(map[string]threadSearchSource, len(sessions))
	for _, sess := range sessions {
		if sess.Visibility == pluginhost.SessionVisibilityPlugin {
			continue
		}
		if sess.ArchivedAt != nil {
			continue
		}
		entry := threadEntryFromSession(sess, s.rt.ProviderName, s.rt.Model)
		sourcesByID[sess.ID] = threadSearchSource{entry: entry}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, th := range s.threads {
		th.mu.Lock()
		thread := th.snapshotLocked()
		visibility := th.Visibility
		entry := threadListEntry{thread: thread, pinnedAt: th.PinnedAt}
		th.mu.Unlock()
		if visibility == pluginhost.SessionVisibilityPlugin {
			delete(sourcesByID, thread.ID)
			continue
		}
		if thread.Ephemeral {
			continue
		}
		if thread.ReadOnly {
			continue
		}
		if thread.Archived {
			delete(sourcesByID, thread.ID)
			continue
		}
		sourcesByID[thread.ID] = threadSearchSource{
			entry: entry,
			live:  th,
		}
	}

	sources := make([]threadSearchSource, 0, len(sourcesByID))
	for _, source := range sourcesByID {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		return threadListEntryLess(sources[i].entry, sources[j].entry)
	})
	return sources, nil
}

// Live history takes precedence over disk, including unpersisted turns. Clone
// only the conversation being searched, outside the server-wide lock.
func (s *Server) threadSearchHistory(source threadSearchSource, db *sql.DB) ([]providers.ChatMessage, error) {
	if source.live != nil {
		source.live.mu.Lock()
		defer source.live.mu.Unlock()
		return cloneHistory(source.live.History), nil
	}
	snapshot, err := session.LoadProviderHistorySnapshotFromStore(db, source.entry.thread.ID)
	var history []providers.ChatMessage
	if err == nil {
		records, convertErr := persistedMessagesFromProviderSnapshot(snapshot, false)
		if convertErr != nil {
			return nil, convertErr
		}
		history = chatMessagesFromPersistedMessages(records)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return history, err
}

type threadSearchResultEntry struct {
	entry  threadListEntry
	result ThreadSearchResultItem
	rank   int
}

func sortThreadSearchResults(results []threadSearchResultEntry) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].rank != results[j].rank {
			return results[i].rank > results[j].rank
		}
		return threadListEntryLess(results[i].entry, results[j].entry)
	})
}

func threadListEntryLess(left, right threadListEntry) bool {
	leftPinned := left.pinnedAt != nil
	rightPinned := right.pinnedAt != nil
	if leftPinned != rightPinned {
		return leftPinned
	}
	leftTime := threadListEntryTime(left)
	rightTime := threadListEntryTime(right)
	if !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	return left.thread.ID > right.thread.ID
}

func threadListEntryTime(entry threadListEntry) time.Time {
	updatedAt := entry.thread.UpdatedAt
	if updatedAt.IsZero() {
		return entry.thread.CreatedAt
	}
	return updatedAt
}

func threadSearchCandidates(thread Thread, history []providers.ChatMessage) []threadSearchCandidate {
	candidates := []threadSearchCandidate{
		{text: thread.Title, rank: threadSearchTitleRank},
		{text: thread.Preview, rank: threadSearchConversationRank},
		{text: thread.Model, rank: threadSearchTechnicalRank},
	}
	if strings.TrimSpace(thread.Title) == "" {
		candidates[1].rank = threadSearchTitleRank // Preview is the displayed fallback title.
	}
	for _, msg := range history {
		if msg.Hidden {
			continue
		}
		candidates = append(candidates, threadSearchCandidatesFromMessage(msg)...)
	}
	return candidates
}

func threadSearchCandidatesFromMessage(msg providers.ChatMessage) []threadSearchCandidate {
	candidates := make([]threadSearchCandidate, 0, 4+len(msg.ToolCalls)*3)
	rank := threadSearchTechnicalRank
	add := func(text string) {
		if text != "" {
			candidates = append(candidates, threadSearchCandidate{text: text, rank: rank})
		}
	}
	if msg.Role == "user" || msg.Role == "assistant" {
		rank = threadSearchConversationRank
	}
	add(msg.DisplayContent)
	add(msg.Content)
	rank = threadSearchTechnicalRank
	add(msg.ReasoningContent)
	add(msg.Name)
	for _, call := range msg.ToolCalls {
		add(call.Name)
		add(call.Arguments)
		if call.Display != nil {
			add(call.Display.Text)
			add(call.Display.Kind)
		}
	}
	if len(msg.Images) == 1 {
		add("[Image #1]")
	} else if len(msg.Images) > 1 {
		add("images")
	}
	if len(msg.Files) == 1 {
		add(filePreview(msg.Files[0], 1))
	} else if len(msg.Files) > 1 {
		add("files")
	}
	return candidates
}

func threadSearchMatchSnippet(candidates []threadSearchCandidate, query string) (string, int) {
	var best threadSearchCandidate
	for _, candidate := range candidates {
		if candidate.rank > best.rank && strings.Contains(normalizeThreadSearchText(candidate.text), query) {
			best = candidate
		}
	}
	return threadSearchExcerpt(best.text, query), best.rank
}

func threadSearchDefaultSnippet(candidates []threadSearchCandidate) string {
	for _, candidate := range candidates {
		if compact := compactThreadSearchText(candidate.text); compact != "" {
			return threadSearchExcerpt(compact, "")
		}
	}
	return ""
}

func threadSearchExcerpt(text, normalizedQuery string) string {
	compact := compactThreadSearchText(text)
	if compact == "" {
		return ""
	}
	runes := []rune(compact)
	if len(runes) <= threadSearchSnippetRunes {
		return compact
	}
	matchIndex := -1
	normalized := ""
	if normalizedQuery != "" {
		normalized = normalizeThreadSearchText(compact)
		matchIndex = strings.Index(normalized, normalizedQuery)
	}
	start := 0
	if matchIndex > 0 {
		start = max(0, len([]rune(normalized[:matchIndex]))-40)
	}
	end := min(len(runes), start+threadSearchSnippetRunes)
	prefix := ""
	if start > 0 {
		prefix = "..."
	}
	suffix := ""
	if end < len(runes) {
		suffix = "..."
	}
	return prefix + string(runes[start:end]) + suffix
}

func normalizeThreadSearchText(value string) string {
	return strings.ToLower(compactThreadSearchText(value))
}

func compactThreadSearchText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
