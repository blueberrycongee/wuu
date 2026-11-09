package context

import (
	"strconv"
	"strings"
	"testing"
)

func TestCompileBlocksRendersTypedContext(t *testing.T) {
	got := CompileBlocks([]Block{
		{Kind: BlockProjectRules, Title: "Rules", Source: "AGENTS.md", Content: "Use gofmt.", TokenBudget: 200},
		{Kind: BlockActiveFiles, Content: "   "},
		{Kind: BlockTestFailures, Content: "go test failed"},
	})

	for _, want := range []string{
		"[PROJECT_RULES]",
		"title: Rules",
		"source: AGENTS.md",
		"token_budget: 200",
		"Use gofmt.",
		"[TEST_FAILURES]",
		"go test failed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compiled context missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[ACTIVE_FILES]") {
		t.Fatalf("empty blocks should be skipped:\n%s", got)
	}
}

func TestCompileRequestBlocksOmitsRuntimeMetadata(t *testing.T) {
	got := CompileRequestBlocks([]Block{{
		Kind: BlockTaskState, Title: "Task", Source: "runtime", Content: "state: active", TokenBudget: 200,
	}})
	if !strings.Contains(got, "[TASK_STATE]\nstate: active") {
		t.Fatalf("compact request block missing kind or content:\n%s", got)
	}
	for _, omitted := range []string{"title:", "source:", "token_budget:"} {
		if strings.Contains(got, omitted) {
			t.Fatalf("compact request block should omit %q:\n%s", omitted, got)
		}
	}
}

func TestIsDerivedLedgerBlockName(t *testing.T) {
	nameFor := func(kind BlockKind, source string) string {
		return SystemReminderBlockMessageName(Block{Kind: kind, Title: "T", Source: source, Content: "x"}, 0)
	}
	for _, ledger := range derivedLedgerBlockIdentities {
		if !IsDerivedLedgerBlockName(nameFor(ledger.kind, ledger.source)) {
			t.Fatalf("expected derived ledger match for %s/%s", ledger.kind, ledger.source)
		}
	}
	kept := []struct {
		kind   BlockKind
		source string
	}{
		{BlockTaskState, "session.summary"},
		{BlockTaskState, "session.checkpoint"},
		{BlockTaskState, "session.notes"},
		{BlockTaskState, "runtime.subagent_status"},
		{BlockTaskState, "runtime"},
		{BlockActiveFiles, "runtime.active_files"},
		{BlockTestFailures, "bash"},
		{BlockToolPolicy, "ultra"},
		{BlockMemory, "session.notes"},
	}
	for _, block := range kept {
		if IsDerivedLedgerBlockName(nameFor(block.kind, block.source)) {
			t.Fatalf("kept block %s/%s must not match derived ledgers", block.kind, block.source)
		}
	}
	for _, nonBlock := range []string{"", "wuu_system_reminder", "wuu_agent_notification"} {
		if IsDerivedLedgerBlockName(nonBlock) {
			t.Fatalf("non-block name %q must not match derived ledgers", nonBlock)
		}
	}
}

func TestCompileBlocksEnforcesTokenBudget(t *testing.T) {
	longContent := strings.Repeat("src/internal/really/long/path/to/file.go\n", 200)
	got := CompileBlocks([]Block{
		{Kind: BlockActiveFiles, Title: "Active files", Source: "runtime.active_files", Content: longContent, TokenBudget: 40},
	})

	for _, want := range []string{
		"[ACTIVE_FILES]",
		"token_budget: 40",
		"truncated: block content exceeded token_budget 40;",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("compiled context missing %q:\n%s", want, got)
		}
	}
	if len(got) >= len(longContent) {
		t.Fatalf("expected content to be truncated, got %d chars from %d-char input", len(got), len(longContent))
	}
	if strings.Count(got, "src/internal/really/long/path/to/file.go") >= 200 {
		t.Fatalf("expected repeated paths to be truncated:\n%s", got)
	}
}

func TestFormatSystemReminderUsesTypedEnvironmentBlock(t *testing.T) {
	got := FormatSystemReminder(EnvInfo{
		CWD:       "/repo",
		Date:      "2026-06-09",
		GitBranch: "main",
		GitStatus: "clean",
	}, "# Extra\n\nUse targeted tests.")

	for _, want := range []string{
		"<system-reminder>",
		"[ENVIRONMENT]",
		"source: runtime.snapshot",
		"- CWD: /repo",
		"[ADDITIONAL_CONTEXT]",
		"Use targeted tests.",
		"</system-reminder>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("system reminder missing %q:\n%s", want, got)
		}
	}
}

func TestSystemReminderBlockMessageNameIsStableAndInternal(t *testing.T) {
	block := Block{
		Kind:    BlockActiveFiles,
		Title:   "Active files",
		Source:  "runtime.active_files",
		Content: "files:\n- go.mod",
	}
	name := SystemReminderBlockMessageName(block, 0)
	if name == "" || len(name) > 64 {
		t.Fatalf("unexpected context message name length: %q", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		t.Fatalf("context message name should be provider-safe, got %q", name)
	}
	changed := block
	changed.Content = "files_scanned: 99"
	if got := SystemReminderBlockMessageName(changed, 0); got != name {
		t.Fatalf("content changes should not change context message name: got %q want %q", got, name)
	}
	if got := SystemReminderBlockMessageName(block, 1); got == name {
		t.Fatalf("duplicate block ordinals should get distinct names: %q", got)
	}
	if !IsSystemReminder(name, "plain content") {
		t.Fatalf("split context message name should be recognized as internal")
	}
}

func TestIsAgentNotificationDetectsNamedAndLegacyHandoffs(t *testing.T) {
	rawNotification := `<subagent_notification>
{"agent_path":"/root/worker","status":{"type":"agent_result","result":"done"}}
</subagent_notification>`
	envelope := `{"author":"/root/worker","recipient":"/root","content":` + strconv.Quote(rawNotification) + `,"trigger_turn":true}`

	cases := []struct {
		name    string
		msgName string
		content string
		want    bool
	}{
		{name: "named", msgName: AgentNotificationMessageName, content: "anything", want: true},
		{name: "raw notification", content: rawNotification, want: true},
		{name: "inter-agent envelope", content: envelope, want: true},
		{name: "plain inter-agent message envelope", content: `{"author":"/root/review_plugin_platform","recipient":"/root","content":"continue with the desktop loader","trigger_turn":false}`, want: true},
		{name: "envelope with overlap sibling (named)", msgName: AgentNotificationMessageName, content: `{"author":"/root/worker","recipient":"/root","content":` + strconv.Quote(rawNotification) + `,"trigger_turn":true,"changed_file_overlap":["changed_file_overlap: foo.go touched by /root/a, /root/b"]}`, want: true},
		{name: "envelope with overlap sibling (unnamed)", content: `{"author":"/root/worker","recipient":"/root","content":` + strconv.Quote(rawNotification) + `,"trigger_turn":true,"changed_file_overlap":["changed_file_overlap: foo.go touched by /root/a, /root/b"]}`, want: true},
		{name: "normal user json", content: `{"content":"plain user text"}`, want: false},
		{name: "normal user json with unrelated author", content: `{"author":"customer","recipient":"support","content":"plain user text"}`, want: false},
		{name: "normal user text", content: "please inspect this", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAgentNotification(tc.msgName, tc.content); got != tc.want {
				t.Fatalf("IsAgentNotification() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsProcessNotificationDetectsInternalCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    bool
	}{
		{name: ProcessNotificationMessageName, content: "anything", want: true},
		{content: `<process_notification>{"process_id":"proc-1"}</process_notification>`, want: true},
		{content: `<process_notification>{"process_id":"proc-1"}`, want: false},
		{content: "ordinary user message", want: false},
	} {
		if got := IsProcessNotification(tc.name, tc.content); got != tc.want {
			t.Fatalf("IsProcessNotification(%q, %q) = %v, want %v", tc.name, tc.content, got, tc.want)
		}
	}
}
