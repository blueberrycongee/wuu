package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ForkHistory copies native history at a verified boundary without mutating
// the source session. OpenCode exposes a message-level cutoff; ACP agents
// that advertise sessionCapabilities.fork copy the whole session and are
// therefore only safe at the end boundary.
func (e *Engine) ForkHistory(ctx context.Context, ref, cwd string, retained, removed []providers.ChatMessage, wholeTurn, endBoundary bool) (string, error) {
	if e == nil || strings.TrimSpace(ref) == "" {
		return "", errors.New("external engine history has no native session reference; its context cannot be safely forked")
	}
	switch e.entry.Protocol {
	case "opencode":
		return e.forkOpenCodeHistory(ctx, ref, cwd, retained, removed, wholeTurn)
	case "acp":
		return e.forkACPHistory(ctx, ref, cwd, removed, endBoundary)
	default:
		return "", fmt.Errorf("%s does not expose a verified native history cutoff; original history is unchanged", e.entry.Name)
	}
}

// forkOpenCodeHistory forks through POST /session/{id}/fork, which keeps the
// messages before an optional messageID cutoff. Wuu and the agent share no
// persisted message identity, so the retained prefix is matched to native
// user messages by position and prompt text before a cutoff is chosen.
func (e *Engine) forkOpenCodeHistory(ctx context.Context, ref, cwd string, retained, removed []providers.ChatMessage, wholeTurn bool) (string, error) {
	if cwd == "" {
		cwd = e.root
	}
	s := &Session{engine: e, binding: agentengine.ThreadBinding{RootDir: cwd}}
	forked := ""
	err := s.withOpenCodeServer(ctx, func(ctx context.Context, c *openCodeClient) error {
		var health struct {
			Healthy bool   `json:"healthy"`
			Version string `json:"version"`
		}
		if err := c.call(ctx, "GET", "/global/health", nil, &health); err != nil {
			return err
		}
		if !health.Healthy || !strings.HasPrefix(health.Version, "1.") {
			return fmt.Errorf("unsupported OpenCode server version %q; this integration requires 1.x", health.Version)
		}
		var messages []openCodeMessage
		if err := c.call(ctx, "GET", "/session/"+url.PathEscape(ref)+"/message", nil, &messages); err != nil {
			return err
		}
		cutoff, err := openCodeForkCutoff(messages, retained, removed, wholeTurn)
		if err != nil {
			return err
		}
		body := map[string]string{}
		if cutoff != "" {
			body["messageID"] = cutoff
		}
		var session struct {
			ID string `json:"id"`
		}
		if err := c.call(ctx, "POST", "/session/"+url.PathEscape(ref)+"/fork", body, &session); err != nil {
			return err
		}
		if session.ID == "" || session.ID == ref {
			return errors.New("OpenCode did not create an independent native history")
		}
		forked = session.ID
		return nil
	})
	if err != nil {
		return "", err
	}
	return forked, nil
}

// openCodeForkCutoff returns the ID of the first native message that must not
// be copied, or "" when the retained prefix spans the entire session. The
// cutoff only exists at user-prompt and turn boundaries; a mid-turn item
// cannot be isolated because OpenCode stores a turn's output in one message.
func openCodeForkCutoff(native []openCodeMessage, retained, removed []providers.ChatMessage, wholeTurn bool) (string, error) {
	var users []int
	for i, message := range native {
		if message.Info.Role == "user" {
			users = append(users, i)
		}
	}
	var prompts []providers.ChatMessage
	for _, message := range retained {
		if message.Role == "system" || message.Role == "meta" {
			continue
		}
		if message.Role != "user" || message.Steered {
			continue
		}
		prompts = append(prompts, message)
	}
	if len(prompts) > len(users) {
		return "", errors.New("visible history is longer than native history; cannot safely copy its context")
	}
	for i, prompt := range prompts {
		if openCodeUserText(native[users[i]]) != prompt.Content {
			return "", errors.New("visible and native history have different turn prefixes; cannot safely copy the context")
		}
	}
	if removed != nil {
		var first *providers.ChatMessage
		for i := range removed {
			if removed[i].Role == "user" && !removed[i].Steered {
				first = &removed[i]
				break
			}
		}
		if first == nil || len(prompts) >= len(users) || openCodeUserText(native[users[len(prompts)]]) != first.Content {
			return "", errors.New("the edited prompt does not match the next native turn")
		}
		return native[users[len(prompts)]].Info.ID, nil
	}
	if wholeTurn {
		if len(prompts) == len(users) {
			return "", nil
		}
		return native[users[len(prompts)]].Info.ID, nil
	}
	if len(prompts) == 0 {
		if len(native) == 0 {
			return "", nil
		}
		return "", errors.New("empty visible history does not describe the existing native context")
	}
	if last := retained[len(retained)-1]; last.Role == "user" && !last.Steered {
		if users[len(prompts)-1]+1 < len(native) {
			return native[users[len(prompts)-1]+1].Info.ID, nil
		}
		return "", nil
	}
	return "", errors.New("OpenCode can fork only at a user message or a completed turn; choose the turn's final reply")
}

// openCodeUserText is the text a user prompt carried into native history.
// Wuu sends exactly one text part per prompt; any additional text parts a
// client added are joined so a multi-part prompt cannot match only a prefix.
func openCodeUserText(message openCodeMessage) string {
	var text strings.Builder
	for _, part := range message.Parts {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

// forkACPHistory uses the unstable ACP session/fork method. It copies the
// whole native session, so the retained prefix must reach the session's
// current end; anything shorter would silently widen the copied context.
func (e *Engine) forkACPHistory(ctx context.Context, ref, cwd string, removed []providers.ChatMessage, endBoundary bool) (string, error) {
	if removed != nil || !endBoundary {
		return "", fmt.Errorf("%s cannot verify a partial native history boundary; choose the end of the session", e.entry.Name)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cwd == "" {
		cwd = e.root
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	p, err := startChild(e.binary, e.entry.LaunchArgs(e.binary), cwd, e.acpEnvironment())
	if err != nil {
		return "", err
	}
	defer p.close()
	r := &rpc{child: p, handle: func(_ context.Context, method string, _ json.RawMessage, request bool) (any, error) {
		if request {
			return nil, &rpcError{Code: -32601, Message: "unsupported engine request: " + method}
		}
		return nil, nil
	}}
	init, err := initializeACP(ctx, r)
	if err != nil {
		return "", acpEngineError(e.entry, p, err)
	}
	if init.Capabilities.Session.Fork == nil {
		return "", fmt.Errorf("%s does not expose a verified native history cutoff; original history is unchanged", e.entry.Name)
	}
	var forked acpSession
	if err := r.session(ctx, "session/fork", map[string]any{"sessionId": ref, "cwd": cwd, "mcpServers": []any{}}, &forked); err != nil {
		return "", fmt.Errorf("session/fork: %w", err)
	}
	// The forked session is the product of this call: release the agent's
	// lock on it when possible, but never delete it.
	_ = r.closeACPSession(init, forked.ID)
	forked.ID = strings.TrimSpace(forked.ID)
	if forked.ID == "" || forked.ID == ref {
		return "", fmt.Errorf("%s did not create an independent native history", e.entry.Name)
	}
	return forked.ID, nil
}
