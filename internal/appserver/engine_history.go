package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Native histories stay owned by their engines. A visible transcript is not a
// replay substitute for tool state, hidden context, or native turn identity.
func (s *Server) forkNativeHistory(ctx context.Context, binding *runtime.State, engineID, ref, cwd string, retained, removed []providers.ChatMessage, boundary agentengine.HistoryForkBoundary) (string, error) {
	id := agentengine.NormalizeEngineID(engineID)
	// A never-run empty thread has no native history to copy.
	if ref == "" && removed == nil {
		empty := true
		for _, message := range retained {
			if message.Role != "system" && message.Role != "meta" {
				empty = false
				break
			}
		}
		if empty {
			return "", nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result pluginprotocol.EngineHistoryForkResult
	err := binding.CallEngine(ctx, id, "history_fork", pluginprotocol.EngineHistoryForkRequest{Ref: ref, CWD: cwd, Retained: historyForkWireMessages(retained), Removed: historyForkWireMessages(removed), WholeTurn: boundary.WholeTurn, TurnBoundary: boundary.TurnBoundary, EndBoundary: boundary.EndBoundary}, &result)
	return result.Ref, err
}

// historyForkWireMessages projects visible messages onto the engine-boundary
// identity shape: native turn/item ids, the steered marker, and a content
// digest. The digest lets engines verify their own native text; visible
// content itself never crosses because it cannot rebuild native state.
func historyForkWireMessages(messages []providers.ChatMessage) []pluginprotocol.EngineHistoryMessage {
	if messages == nil {
		return nil
	}
	out := make([]pluginprotocol.EngineHistoryMessage, len(messages))
	for i, message := range messages {
		out[i] = pluginprotocol.EngineHistoryMessage{
			Role:           message.Role,
			EngineTurnID:   message.EngineTurnID,
			ProviderItemID: message.ProviderItemID,
			Steered:        message.Steered,
			ContentSHA256:  historyContentDigest(message.Content),
		}
	}
	return out
}

func historyContentDigest(content string) string {
	if content == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
