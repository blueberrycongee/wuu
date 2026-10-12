package codexengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type historyThread struct {
	ID    string        `json:"id"`
	Turns []historyTurn `json:"turns"`
}

type historyTurn struct {
	ID        string            `json:"id"`
	Status    string            `json:"status"`
	Items     []json.RawMessage `json:"items"`
	ItemsView string            `json:"itemsView"`
}

// ForkHistory creates an independent native prefix without mutating ref. Codex
// supports whole-turn boundaries, not arbitrary message/tool-item cuts. removed
// is supplied only for editing: its first prompt and all later turns are cut.
// Returned history is checked because older servers may ignore unknown cutoff
// fields rather than reject them. Never accept such a full-history fork.
func (e *Engine) ForkHistory(ctx context.Context, ref, cwd string, retained, removed []providers.ChatMessage, wholeTurn bool) (string, error) {
	if e == nil || e.host == nil || strings.TrimSpace(ref) == "" {
		return "", errors.New("Codex history has no native session reference; its context cannot be safely forked")
	}
	client, release, err := e.host.Acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	var source struct {
		Thread historyThread `json:"thread"`
	}
	if err := client.Request(ctx, "thread/read", map[string]any{"threadId": ref, "includeTurns": true}, &source); err != nil {
		return "", fmt.Errorf("read native history: %w", err)
	}
	if source.Thread.ID != ref || source.Thread.Turns == nil {
		return "", errors.New("Codex did not return complete native history; update the CLI before forking or editing")
	}
	for _, turn := range source.Thread.Turns {
		if turn.ID == "" || turn.Items == nil || (turn.ItemsView != "" && turn.ItemsView != "full") {
			return "", errors.New("Codex returned incomplete turn history; cannot verify a safe cutoff")
		}
	}
	// Resolve provider output identities once. Native tool-heavy histories can
	// contain many items per turn; repeatedly decoding the entire prefix makes
	// a history-only operation quadratic in its size.
	itemTurns := make(map[string]string)
	ambiguousItems := make(map[string]bool)
	for _, turn := range source.Thread.Turns {
		for _, raw := range turn.Items {
			var item struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				return "", err
			}
			if item.ID == "" {
				continue
			}
			if _, exists := itemTurns[item.ID]; exists {
				ambiguousItems[item.ID] = true
			}
			itemTurns[item.ID] = turn.ID
		}
	}
	// A durable user-to-native-turn mapping is authoritative. Legacy turns can
	// still be proven by a provider-native output ID; text/order alone is not an
	// identity and is deliberately never used to guess a native turn.
	resolveTurn := func(messages []providers.ChatMessage) (string, error) {
		id := ""
		for _, message := range messages {
			if message.EngineTurnID != "" {
				if id != "" && id != message.EngineTurnID {
					return "", errors.New("history spans more than one native turn")
				}
				id = message.EngineTurnID
			}
			if message.Role != "assistant" || message.ProviderItemID == "" {
				continue
			}
			if ambiguousItems[message.ProviderItemID] {
				return "", errors.New("native output identity is ambiguous")
			}
			found := itemTurns[message.ProviderItemID]
			if found == "" || (id != "" && id != found) {
				return "", errors.New("visible history no longer matches its native turn; cannot safely fork or edit")
			}
			id = found
		}
		if id == "" {
			return "", errors.New("this historical turn has no verifiable native identity; choose a completed turn with native history")
		}
		return id, nil
	}
	var segments [][]providers.ChatMessage
	for _, message := range retained {
		if message.Role == "system" || message.Role == "meta" {
			continue
		}
		if message.Role == "user" && !message.Steered {
			segments = append(segments, nil)
		}
		if len(segments) == 0 {
			return "", errors.New("visible history is missing its native prompt boundary")
		}
		segments[len(segments)-1] = append(segments[len(segments)-1], message)
	}
	if len(segments) > len(source.Thread.Turns) {
		return "", errors.New("visible history is longer than native history; cannot safely copy its context")
	}
	for i, segment := range segments {
		id, err := resolveTurn(segment)
		if err != nil {
			return "", err
		}
		turn := source.Thread.Turns[i]
		if turn.ID != id {
			return "", errors.New("visible and native history have different turn prefixes; cannot safely copy the context")
		}
		if turn.Status != "completed" && turn.Status != "interrupted" && turn.Status != "failed" {
			return "", errors.New("wait for the native turn to finish before forking its history")
		}
	}
	keep := len(segments)
	params := map[string]any{"threadId": ref, "cwd": cwd}
	if removed != nil {
		end := 1
		for end < len(removed) && (removed[end].Role != "user" || removed[end].Steered) {
			end++
		}
		targetID, err := resolveTurn(removed[:end])
		if err != nil {
			return "", err
		}
		if keep >= len(source.Thread.Turns) || source.Thread.Turns[keep].ID != targetID {
			return "", errors.New("the edited prompt does not match the next native turn")
		}
		if keep == 0 {
			params["beforeTurnId"] = targetID
		} else {
			params["lastTurnId"] = source.Thread.Turns[keep-1].ID
		}
	} else if keep > 0 {
		if !wholeTurn {
			last := segments[keep-1][len(segments[keep-1])-1]
			turn := source.Thread.Turns[keep-1]
			var item struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if len(turn.Items) > 0 {
				_ = json.Unmarshal(turn.Items[len(turn.Items)-1], &item)
			}
			if last.Role != "assistant" || last.ProviderItemID == "" || item.ID != last.ProviderItemID || item.Type != "agentMessage" {
				return "", errors.New("Codex can fork only at a complete native turn; choose its final reply, not a user message, commentary, or tool item")
			}
		}
		params["lastTurnId"] = source.Thread.Turns[keep-1].ID
	} else if len(source.Thread.Turns) > 0 {
		return "", errors.New("empty visible history does not describe the existing native context")
	}
	var fork struct {
		Thread historyThread `json:"thread"`
	}
	if err := client.Request(ctx, "thread/fork", params, &fork); err != nil {
		return "", fmt.Errorf("fork native history (requires bounded thread/fork support): %w", err)
	}
	fork.Thread.ID = strings.TrimSpace(fork.Thread.ID)
	if fork.Thread.ID == "" || fork.Thread.ID == strings.TrimSpace(ref) {
		return "", errors.New("Codex did not create an independent native history")
	}
	// Native forks auto-subscribe this connection and otherwise stay loaded in
	// the warm host. Release that subscription even if verification or the later
	// Wuu commit fails. The native audit history remains resumable on disk.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), nativeCleanupTimeout)
		defer cancel()
		if err := client.Request(cleanupCtx, "thread/unsubscribe", map[string]any{"threadId": fork.Thread.ID}, nil); err != nil {
			providers.DebugLogf("unsubscribe native history fork %q: %v", fork.Thread.ID, err)
		}
	}()
	if fork.Thread.Turns == nil || len(fork.Thread.Turns) != keep {
		return "", errors.New("Codex did not honor the requested history cutoff; original history is unchanged, update the CLI before retrying")
	}
	for i, turn := range fork.Thread.Turns {
		expected := source.Thread.Turns[i]
		if turn.ID != expected.ID || turn.Status != expected.Status || turn.Items == nil || (turn.ItemsView != "" && turn.ItemsView != "full") || len(turn.Items) != len(expected.Items) {
			return "", errors.New("Codex fork did not preserve the selected native turns; original history is unchanged")
		}
		for j, raw := range turn.Items {
			var got, want any
			gotDecoder := json.NewDecoder(bytes.NewReader(raw))
			wantDecoder := json.NewDecoder(bytes.NewReader(expected.Items[j]))
			// Native MCP structured output may contain integers above 2^53.
			// float64 decoding would silently accept adjacent, unequal values.
			gotDecoder.UseNumber()
			wantDecoder.UseNumber()
			if gotDecoder.Decode(&got) != nil || wantDecoder.Decode(&want) != nil || !reflect.DeepEqual(got, want) {
				return "", errors.New("Codex fork did not preserve native turn items; original history is unchanged")
			}
		}
	}
	return fork.Thread.ID, nil
}
