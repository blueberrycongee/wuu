package appserver

import (
	"context"

	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Native histories stay owned by their engines. A visible transcript is not a
// replay substitute for tool state, hidden context, or native turn identity.
func (s *Server) forkNativeHistory(ctx context.Context, binding *runtime.State, engineID, ref, cwd string, retained, removed []providers.ChatMessage, wholeTurn bool) (string, error) {
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
	convert := func(messages []providers.ChatMessage) []pluginprotocol.EngineHistoryMessage {
		if messages == nil {
			return nil
		}
		out := make([]pluginprotocol.EngineHistoryMessage, len(messages))
		for i, message := range messages {
			out[i] = pluginprotocol.EngineHistoryMessage{Role: message.Role, EngineTurnID: message.EngineTurnID, ProviderItemID: message.ProviderItemID, Steered: message.Steered}
		}
		return out
	}
	var result pluginprotocol.EngineHistoryForkResult
	err := binding.CallEngine(ctx, id, "history_fork", pluginprotocol.EngineHistoryForkRequest{Ref: ref, CWD: cwd, Retained: convert(retained), Removed: convert(removed), WholeTurn: wholeTurn}, &result)
	return result.Ref, err
}
