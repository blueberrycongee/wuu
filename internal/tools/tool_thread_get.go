package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

const threadGetName = "thread_get"

// ThreadGetTool looks up a session (thread) by ID and returns its full
// data: metadata + history records. The thread ID is the value copied
// from the desktop session tree's right-click menu; this tool is what
// makes "copy an ID, paste it to an agent" actually useful — the agent
// resolves the pasted ID back to the full conversation.
//
// Reads use the session store captured by the owning runtime generation.
type ThreadGetTool struct {
	store *session.Store
}

func NewThreadGetTool(store *session.Store) *ThreadGetTool {
	return &ThreadGetTool{store: store}
}

func (t *ThreadGetTool) Name() string { return threadGetName }

func (t *ThreadGetTool) IsReadOnly() bool        { return true }
func (t *ThreadGetTool) IsConcurrencySafe() bool { return true }

func (t *ThreadGetTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: threadGetName,
		Description: "thread_get looks up a Wuu thread ID / session ID and returns a bounded semantic page: a context anchor plus recent complete turns. Use page.next to read the next older non-overlapping page. " +
			"Use this when a user pastes a thread ID or asks you to investigate a conversation by ID. " +
			"Do not inspect legacy workspace session directories or guess session files; this tool reads the configured session store.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"thread_id"},
			"properties": map[string]any{
				"thread_id": map[string]any{
					"type":        "string",
					"description": "Thread (session) ID, e.g. 20260618-045400-3a8e9b1c0d2f",
				},
				"before_seq": map[string]any{
					"type":        "integer",
					"description": "Exclusive upper sequence bound for an older page. For continuation, use page.next.before_seq.",
				},
				"snapshot_seq": map[string]any{
					"type":        "integer",
					"description": "Required with before_seq; use page.next.snapshot_seq.",
				},
				"expected_snapshot": map[string]any{
					"type":        "string",
					"description": "Required with before_seq; use page.next.expected_snapshot to reject rewritten history.",
				},
			},
		},
	}
}

func (t *ThreadGetTool) Classify(argsJSON string) ToolClassification {
	var args struct {
		ThreadID string `json:"thread_id"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	trimmed := strings.TrimSpace(args.ThreadID)
	reason := "thread_get is read-only"
	if trimmed == "" {
		reason = "thread_get with empty thread_id returns ErrSessionNotFound before any IO"
	}
	return ToolClassification{
		ReadOnly:        true,
		ConcurrencySafe: true,
		Risk:            ToolRiskLow,
		Reason:          reason,
	}
}

func (t *ThreadGetTool) Execute(ctx context.Context, args string) (string, error) {
	var a struct {
		ThreadID         string `json:"thread_id"`
		BeforeSeq        int    `json:"before_seq"`
		SnapshotSeq      int    `json:"snapshot_seq"`
		ExpectedSnapshot string `json:"expected_snapshot"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", fmt.Errorf("thread_get: %w", err)
	}
	id := strings.TrimSpace(a.ThreadID)
	if id == "" {
		return "", fmt.Errorf("thread_get: thread_id is required")
	}
	if a.BeforeSeq < 0 || a.SnapshotSeq < 0 {
		return "", fmt.Errorf("thread_get: before_seq and snapshot_seq must be non-negative")
	}
	if a.BeforeSeq > 0 && (a.SnapshotSeq == 0 || strings.TrimSpace(a.ExpectedSnapshot) == "") {
		return "", fmt.Errorf("thread_get: snapshot_seq and expected_snapshot are required with before_seq")
	}

	if t.store == nil {
		return "", errors.New("thread_get: session store is unavailable")
	}
	store := t.store.WithContext(ctx)
	meta, ok, err := store.Find(id)
	if err != nil {
		return "", fmt.Errorf("thread_get: lookup %q: %w", id, err)
	}
	if !ok {
		return "", fmt.Errorf("%w: %q", session.ErrSessionNotFound, id)
	}

	records, err := store.LoadHistoryRecords(id, true)
	if err != nil {
		return "", fmt.Errorf("thread_get: load history %q: %w", id, err)
	}

	latestSeq := 0
	for _, record := range records {
		if record.Seq > latestSeq {
			latestSeq = record.Seq
		}
	}
	snapshotSeq := a.SnapshotSeq
	if snapshotSeq == 0 {
		snapshotSeq = latestSeq
	}
	snapshotRecords := make([]session.HistoryRecord, 0, len(records))
	for _, record := range records {
		if record.Seq <= snapshotSeq {
			snapshotRecords = append(snapshotRecords, record)
		}
	}
	snapshotBytes, err := json.Marshal(snapshotRecords)
	if err != nil {
		return "", fmt.Errorf("thread_get: marshal snapshot: %w", err)
	}
	snapshotHash := sha256.Sum256(snapshotBytes)
	snapshotToken := hex.EncodeToString(snapshotHash[:])
	if expected := strings.TrimSpace(a.ExpectedSnapshot); expected != "" && expected != snapshotToken {
		return "", fmt.Errorf("thread_get: continuation is stale: history before snapshot_seq %d changed; restart without before_seq", snapshotSeq)
	}

	data, err := json.Marshal(map[string]any{
		"thread_id":            id,
		"session":              meta,
		"history":              snapshotRecords,
		"history_record_count": len(snapshotRecords),
		"before_seq":           a.BeforeSeq,
		"snapshot_seq":         snapshotSeq,
		"snapshot_token":       snapshotToken,
	})
	if err != nil {
		return "", fmt.Errorf("thread_get: marshal result: %w", err)
	}
	return string(data), nil
}
