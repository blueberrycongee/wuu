package tui

import (
	"strings"
	"testing"
)

func TestParseAskUserQuestionList(t *testing.T) {
	args := `{"questions":[{"question":"Q1?","header":"H","options":[]},{"question":"Q2?","header":"H","options":[]}]}`
	got := parseAskUserQuestionList(args)
	if len(got) != 2 || got[0] != "Q1?" || got[1] != "Q2?" {
		t.Fatalf("expected [Q1?, Q2?], got %v", got)
	}
}

func TestParseAskUserQuestionList_Malformed(t *testing.T) {
	if got := parseAskUserQuestionList("not json"); got != nil {
		t.Fatalf("expected nil for malformed input, got %v", got)
	}
	if got := parseAskUserQuestionList(""); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestParseAskUserResult_Success(t *testing.T) {
	res := `{"answers":{"Which auth?":"OAuth","Use Redis?":"Yes"}}`
	answers, errMsg := parseAskUserResult(res)
	if errMsg != "" {
		t.Fatalf("expected no error, got %q", errMsg)
	}
	if answers["Which auth?"] != "OAuth" || answers["Use Redis?"] != "Yes" {
		t.Fatalf("unexpected answers: %v", answers)
	}
}

func TestParseAskUserResult_Cancelled(t *testing.T) {
	res := `{"answers":null,"cancelled":true}`
	_, errMsg := parseAskUserResult(res)
	if errMsg != "user cancelled" {
		t.Fatalf("expected cancelled message, got %q", errMsg)
	}
}

func TestParseAskUserResult_Error(t *testing.T) {
	res := `{"error":"ask_user: bridge not configured"}`
	_, errMsg := parseAskUserResult(res)
	if errMsg != "bridge not configured" {
		t.Fatalf("expected stripped error, got %q", errMsg)
	}
}

func TestRenderAskUserCard_CollapsedSummary(t *testing.T) {
	tc := ToolCallEntry{
		Name:      "ask_user",
		Args:      `{"questions":[{"question":"Which auth strategy?","header":"Auth","options":[]}]}`,
		Result:    `{"answers":{"Which auth strategy?":"OAuth"}}`,
		Status:    ToolCallDone,
		Collapsed: true,
	}
	out := renderAskUserCard(tc, 100)
	if !strings.Contains(out, "ask_user") {
		t.Fatalf("expected tool name in output, got: %s", out)
	}
	if !strings.Contains(out, "answered") {
		t.Fatalf("expected status in output, got: %s", out)
	}
	if !strings.Contains(out, "Which auth strategy?") {
		t.Fatalf("expected question in summary, got: %s", out)
	}
}

func TestRenderAskUserCard_ExpandedShowsAnswers(t *testing.T) {
	tc := ToolCallEntry{
		Name:      "ask_user",
		Args:      `{"questions":[{"question":"Q1?","header":"H","options":[]},{"question":"Q2?","header":"H","options":[]}]}`,
		Result:    `{"answers":{"Q1?":"A1","Q2?":"A2"}}`,
		Status:    ToolCallDone,
		Collapsed: false,
	}
	out := renderAskUserCard(tc, 100)
	if !strings.Contains(out, "User answered:") {
		t.Fatalf("expected 'User answered:' header, got: %s", out)
	}
	if !strings.Contains(out, "Q1?") || !strings.Contains(out, "A1") {
		t.Fatalf("expected first Q/A in output, got: %s", out)
	}
	if !strings.Contains(out, "Q2?") || !strings.Contains(out, "A2") {
		t.Fatalf("expected second Q/A in output, got: %s", out)
	}
	// Verify ordering: Q1 should appear before Q2 (matches the
	// order in the args, not random map iteration).
	q1Idx := strings.Index(out, "Q1?")
	q2Idx := strings.Index(out, "Q2?")
	if q1Idx < 0 || q2Idx < 0 || q1Idx > q2Idx {
		t.Fatalf("expected Q1 before Q2 in output, got: %s", out)
	}
}

func TestRenderAskUserCard_ErrorPath(t *testing.T) {
	tc := ToolCallEntry{
		Name:      "ask_user",
		Args:      `{"questions":[{"question":"Q?","header":"H","options":[]}]}`,
		Result:    `{"error":"ask_user: user dismissed the dialog"}`,
		Status:    ToolCallDone,
		Collapsed: false,
	}
	out := renderAskUserCard(tc, 100)
	if !strings.Contains(out, "✗") {
		t.Fatalf("expected error indicator in output, got: %s", out)
	}
	if strings.Contains(out, "User answered:") {
		t.Fatalf("error case should not show answers header, got: %s", out)
	}
}

func TestTruncateInline(t *testing.T) {
	if got := truncateInline("hello", 10); got != "hello" {
		t.Errorf("short string should pass through, got %q", got)
	}
	if got := truncateInline("hello world", 8); got != "hello w…" {
		t.Errorf("expected truncation with ellipsis, got %q", got)
	}
	if got := truncateInline("你好世界你好", 4); got != "你好世…" {
		t.Errorf("expected rune-aware truncation, got %q", got)
	}
}
