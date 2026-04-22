package tui

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ── /help grouping ──────────────────────────────────────────────────

func TestCmdHelp_EmitsGroupHeadersInDeclaredOrder(t *testing.T) {
	out := cmdHelp("", nil)
	// Every declared group that has at least one non-hidden command
	// should appear in commandGroupOrder precedence.
	positions := make(map[string]int)
	for _, g := range commandGroupOrder {
		header := g + ":"
		if i := strings.Index(out, header); i >= 0 {
			positions[g] = i
		}
	}
	if len(positions) < 3 {
		t.Fatalf("expected multiple group headers in help, got %d: %q", len(positions), out)
	}
	// Headers that exist must be in declared order.
	var seen []int
	for _, g := range commandGroupOrder {
		if p, ok := positions[g]; ok {
			seen = append(seen, p)
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] <= seen[i-1] {
			t.Fatalf("group headers out of order at %d in %q", i, out)
		}
	}
}

func TestCmdHelp_ListsAllVisibleCommands(t *testing.T) {
	out := cmdHelp("", nil)
	for _, cmd := range commandRegistry {
		if cmd.Hidden {
			continue
		}
		needle := "/" + cmd.Name
		if !strings.Contains(out, needle) {
			t.Errorf("help missing %q: %q", needle, out)
		}
	}
}

func TestCmdHelp_HiddenCommandsOmitted(t *testing.T) {
	// Temporarily splice a hidden command into the registry to verify
	// it doesn't leak into help output, then restore.
	orig := commandRegistry
	defer func() { commandRegistry = orig }()
	commandRegistry = append([]command(nil), orig...)
	commandRegistry = append(commandRegistry, command{
		Name:        "secret-debug-cmd",
		Group:       "App",
		Description: "should never appear",
		Hidden:      true,
		Type:        cmdTypeLocal,
		Execute:     func(string, *Model) string { return "" },
	})
	out := cmdHelp("", nil)
	if strings.Contains(out, "secret-debug-cmd") {
		t.Fatalf("hidden command leaked into help: %q", out)
	}
}

func TestCmdHelp_UngroupedCommandFallsIntoOtherBucket(t *testing.T) {
	orig := commandRegistry
	defer func() { commandRegistry = orig }()
	commandRegistry = append([]command(nil), orig...)
	commandRegistry = append(commandRegistry, command{
		Name:        "loose-end",
		Description: "no group set on purpose",
		Type:        cmdTypeLocal,
		Execute:     func(string, *Model) string { return "" },
	})
	out := cmdHelp("", nil)
	if !strings.Contains(out, "Other:") {
		t.Fatalf("ungrouped command should land in Other: bucket, got %q", out)
	}
	// "Other:" must appear AFTER every declared group header. Find the
	// last declared-group header and assert "Other:" comes later.
	otherPos := strings.Index(out, "Other:")
	if otherPos < 0 {
		t.Fatal("Other: header missing")
	}
	for _, g := range commandGroupOrder {
		if p := strings.Index(out, g+":"); p >= 0 && p > otherPos {
			t.Fatalf("declared group %q appeared after Other: — got %q", g, out)
		}
	}
}

// ── /queue command ──────────────────────────────────────────────────

func TestCmdQueue_EmptyShowsFriendlyMessage(t *testing.T) {
	m := &Model{}
	if got := cmdQueue("", m); got != "queue is empty" {
		t.Fatalf("want 'queue is empty', got %q", got)
	}
}

func TestCmdQueue_ListsSteersThenQueueWithCombinedIndex(t *testing.T) {
	m := &Model{
		pendingSteers: []queuedMessage{
			{Text: "steer0"},
			{Text: "steer1"},
		},
		messageQueue: []queuedMessage{
			{Text: "queue0"},
		},
	}
	out := cmdQueue("", m)
	if !strings.Contains(out, "0  [steer]  steer0") {
		t.Fatalf("expected steer0 at index 0, got %q", out)
	}
	if !strings.Contains(out, "1  [steer]  steer1") {
		t.Fatalf("expected steer1 at index 1, got %q", out)
	}
	if !strings.Contains(out, "2  [queue]  queue0") {
		t.Fatalf("expected queue0 at index 2 (continues numbering), got %q", out)
	}
}

func TestCmdQueue_Clear(t *testing.T) {
	m := &Model{
		pendingSteers: []queuedMessage{{Text: "a"}},
		messageQueue:  []queuedMessage{{Text: "b"}, {Text: "c"}},
	}
	got := cmdQueue("clear", m)
	if !strings.Contains(got, "cleared 3") {
		t.Fatalf("expected 'cleared 3 queued item(s)', got %q", got)
	}
	if len(m.pendingSteers) != 0 || len(m.messageQueue) != 0 {
		t.Fatalf("clear did not empty buffers: %+v %+v", m.pendingSteers, m.messageQueue)
	}
}

func TestCmdQueue_ClearOnEmpty(t *testing.T) {
	m := &Model{}
	if got := cmdQueue("clear", m); got != "queue is already empty" {
		t.Fatalf("want 'queue is already empty', got %q", got)
	}
}

func TestCmdQueue_RemoveFromSteersByIndex(t *testing.T) {
	m := &Model{
		pendingSteers: []queuedMessage{{Text: "first"}, {Text: "second"}},
		messageQueue:  []queuedMessage{{Text: "third"}},
	}
	out := cmdQueue("rm 0", m)
	if !strings.Contains(out, "removed #0") {
		t.Fatalf("want removal confirmation, got %q", out)
	}
	if len(m.pendingSteers) != 1 || m.pendingSteers[0].Text != "second" {
		t.Fatalf("unexpected steers after rm: %+v", m.pendingSteers)
	}
	if len(m.messageQueue) != 1 {
		t.Fatalf("messageQueue should be untouched: %+v", m.messageQueue)
	}
}

func TestCmdQueue_RemoveCrossesIntoMessageQueue(t *testing.T) {
	// 2 steers + 3 queue items. Index 3 must be the second queue item.
	m := &Model{
		pendingSteers: []queuedMessage{{Text: "s0"}, {Text: "s1"}},
		messageQueue:  []queuedMessage{{Text: "q0"}, {Text: "q1"}, {Text: "q2"}},
	}
	out := cmdQueue("rm 3", m)
	if !strings.Contains(out, "removed #3") {
		t.Fatalf("want removal confirmation, got %q", out)
	}
	if len(m.messageQueue) != 2 ||
		m.messageQueue[0].Text != "q0" ||
		m.messageQueue[1].Text != "q2" {
		t.Fatalf("expected q1 removed, got %+v", m.messageQueue)
	}
}

func TestCmdQueue_RemoveOutOfRange(t *testing.T) {
	m := &Model{
		messageQueue: []queuedMessage{{Text: "only"}},
	}
	out := cmdQueue("rm 9", m)
	if !strings.Contains(out, "out of range") {
		t.Fatalf("want out-of-range message, got %q", out)
	}
	if len(m.messageQueue) != 1 {
		t.Fatal("failed rm must not mutate state")
	}
}

func TestCmdQueue_RemoveRejectsNonInt(t *testing.T) {
	m := &Model{messageQueue: []queuedMessage{{Text: "x"}}}
	if got := cmdQueue("rm foo", m); !strings.Contains(got, "not a valid index") {
		t.Fatalf("want invalid-index message, got %q", got)
	}
	if got := cmdQueue("rm -1", m); !strings.Contains(got, "not a valid index") {
		t.Fatalf("negative index should be rejected, got %q", got)
	}
}

func TestCmdQueue_RemoveWithoutIndex(t *testing.T) {
	m := &Model{messageQueue: []queuedMessage{{Text: "x"}}}
	if got := cmdQueue("rm", m); !strings.Contains(got, "usage: /queue rm") {
		t.Fatalf("expected usage hint, got %q", got)
	}
}

func TestCmdQueue_UnknownSubcommand(t *testing.T) {
	m := &Model{}
	if got := cmdQueue("banana", m); !strings.Contains(got, "unknown /queue subcommand") {
		t.Fatalf("want unknown-subcommand error, got %q", got)
	}
}

// Regression: queued items with images must still show up in the list.
// The summarizer already handles images (it counts them via
// formatUserEntryContent), so we just assert the row isn't dropped.
func TestCmdQueue_ListsItemWithImages(t *testing.T) {
	m := &Model{
		messageQueue: []queuedMessage{{
			Text: "fix this",
			Images: []providers.InputImage{
				{MediaType: "image/png", Data: "abc"},
			},
		}},
	}
	out := cmdQueue("", m)
	if !strings.Contains(out, "0  [queue]") {
		t.Fatalf("image-bearing item missing from list: %q", out)
	}
}
