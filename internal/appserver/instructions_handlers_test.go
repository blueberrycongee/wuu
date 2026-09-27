package appserver

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestHandleInstructionsListEmpty(t *testing.T) {
	var buf bytes.Buffer
	srv := &Server{out: &buf, rt: &runtime.Session{}}
	if err := srv.handleInstructionsList(Request{ID: json.RawMessage(`1`)}); err != nil {
		t.Fatalf("handleInstructionsList: %v", err)
	}
	var resp struct {
		Result InstructionsListResult `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Result.Files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(resp.Result.Files))
	}
	// The list is always a non-nil slice so it serializes to [] rather than null.
	if resp.Result.Files == nil {
		t.Errorf("expected non-nil empty slice")
	}
}
