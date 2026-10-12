package appserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Native histories stay owned by their engines. A visible transcript is not a
// replay substitute for tool state, hidden context, or native turn identity.
func (s *Server) forkNativeHistory(ctx context.Context, engineID, ref, cwd string, retained, removed []providers.ChatMessage, wholeTurn bool) (string, error) {
	id := agentengine.NormalizeEngineID(engineID)
	if id == agentengine.EngineWuu {
		return "", nil
	}
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
	if id != "codex" {
		return "", fmt.Errorf("%s does not expose a verified native history cutoff in Wuu; this fork/edit would lose or retain hidden context, so the original history is unchanged", id)
	}
	if s.rt == nil || s.rt.Engines() == nil {
		return "", errors.New("Codex engine is unavailable; original history is unchanged")
	}
	factory, _ := s.rt.Engines().Lookup(id)
	engine, ok := factory.(*codexengine.Engine)
	if !ok {
		return "", errors.New("Codex engine is unavailable; original history is unchanged")
	}
	s.retainCodexHost()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return engine.ForkHistory(ctx, ref, cwd, retained, removed, wholeTurn)
}
