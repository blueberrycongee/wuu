package appserver

import (
	"errors"
	"os"
	"strings"
	"time"
)

const (
	// defaultThreadPreviewLimit is the number of turns the conversation-search
	// preview pane asks for by default. Four turns is enough to disambiguate
	// "which rate-limiting thread was that" — user + assistant x2 — without
	// forcing the renderer to scroll inside the preview pane.
	defaultThreadPreviewLimit = 4
	// maxThreadPreviewLimit caps the worst case so a curious renderer cannot
	// stream the entire history of a long session through the preview RPC.
	maxThreadPreviewLimit = 16
)

// handleThreadPreview serves the conversation-search preview pane: it loads
// the first N turns of a thread from disk and returns them as a renderable
// Turn slice. Search itself stays light (title + snippet); preview is
// fetched lazily when a result is selected.
//
// A missing history file is treated as an empty preview rather than an
// error: search may surface threads whose on-disk history has not been
// written yet (brand-new sessions, in-memory threads not yet flushed), and
// the preview pane should just show "暂无预览" instead of failing.
func (s *Server) handleThreadPreview(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params ThreadPreviewParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	threadID := strings.TrimSpace(params.ThreadID)
	if threadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultThreadPreviewLimit
	}
	if limit > maxThreadPreviewLimit {
		limit = maxThreadPreviewLimit
	}

	history, err := loadChatMessages(binding.Sessions, threadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.writeResponse(req.ID, ThreadPreviewResult{Turns: []Turn{}}, nil)
		}
		return s.writeResponse(req.ID, nil, err)
	}

	turns := turnsFromHistory(threadID, history, time.Now())
	if len(turns) > limit {
		turns = turns[:limit]
	}
	if turns == nil {
		turns = []Turn{}
	}
	return s.writeResponse(req.ID, ThreadPreviewResult{Turns: turns}, nil)
}
