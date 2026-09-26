package session

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadProviderHistorySnapshotKeepsLegacyHistory(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	legacy := []HistoryRecord{
		{Role: "user", Content: "hello"},
		{Role: "meta", Content: "token_usage", InputTokens: 12},
		{Role: "assistant", Content: "world"},
	}
	for _, rec := range legacy {
		if err := AppendHistoryRecord(dir, "thread-1", rec); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := LoadHistoryRecords(dir, "thread-1", true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadProviderHistorySnapshot(dir, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Checkpoint != nil {
		t.Fatalf("legacy snapshot checkpoint = %+v, want nil", snapshot.Checkpoint)
	}
	if snapshot.HeadSeq != 3 || !reflect.DeepEqual(snapshot.Records, raw) {
		t.Fatalf("legacy snapshot = %+v, want head=3 records=%+v", snapshot, raw)
	}
	if _, ok, err := LatestHistoryCheckpoint(dir, "thread-1"); err != nil || ok {
		t.Fatalf("LatestHistoryCheckpoint() = ok=%v err=%v, want no checkpoint", ok, err)
	}
	if _, err := LoadProviderHistorySnapshot(dir, "missing-thread"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("LoadProviderHistorySnapshot() error = %v, want ErrSessionNotFound", err)
	}
}

func TestStoreHistoryCheckpointIsAppendOnlyAndPreservesConcurrentTail(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	baseline := []HistoryRecord{
		{Role: "user", Content: "original user"},
		{Role: "assistant", Content: "original answer"},
		{Role: "assistant", Content: "original follow-up"},
	}
	for _, rec := range baseline {
		if err := AppendHistoryRecord(dir, "thread-1", rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{Role: "user", Content: "concurrent tail"}); err != nil {
		t.Fatal(err)
	}
	modifiedAt := time.Date(2026, 7, 14, 12, 30, 0, 123, time.UTC)
	replacement := []HistoryRecord{
		{
			Seq: 1, Role: "user", Content: "inline modified user", DisplayContent: "display",
			FinishReason: "stop", ReasoningBlocks: []byte(`[{"type":"text","text":"exact"}]`), At: modifiedAt,
		},
		{
			Role: "Assistant", Content: "new compact summary", StopReason: "end_turn", Truncated: true,
			ProviderItemID: "provider-item", Files: []byte(`[{"filename":"summary.txt","data":"exact"}]`),
		},
	}
	checkpoint, err := StoreHistoryCheckpointAtBaseline(
		dir, "thread-1", HistoryCheckpointKindProviderRewrite, replacement, 3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Version != 1 || checkpoint.Kind != HistoryCheckpointKindProviderRewrite || checkpoint.ThroughSeq != 5 {
		t.Fatalf("checkpoint metadata = %+v, want version=1 through_seq=5", checkpoint)
	}
	if len(checkpoint.Replacement) != 3 {
		t.Fatalf("checkpoint replacement = %+v, want replacement plus captured tail", checkpoint.Replacement)
	}
	wantPrefix := append([]HistoryRecord(nil), replacement...)
	wantPrefix[1].Seq = 5
	if !reflect.DeepEqual(checkpoint.Replacement[:2], wantPrefix) {
		t.Fatalf("checkpoint exact replacement prefix = %+v, want %+v", checkpoint.Replacement[:2], wantPrefix)
	}
	if checkpoint.Replacement[2].Seq != 4 || checkpoint.Replacement[2].Content != "concurrent tail" {
		t.Fatalf("captured tail = %+v, want physical seq 4", checkpoint.Replacement[2])
	}

	raw, err := LoadHistoryRecords(dir, "thread-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 5 {
		t.Fatalf("raw history = %+v, want five append-only rows", raw)
	}
	wantRawContents := []string{"original user", "original answer", "original follow-up", "concurrent tail", "new compact summary"}
	for i, want := range wantRawContents {
		if raw[i].Seq != i+1 || raw[i].Content != want {
			t.Fatalf("raw[%d] = %+v, want seq=%d content=%q", i, raw[i], i+1, want)
		}
	}
	if raw[0].Content == replacement[0].Content {
		t.Fatal("inline replacement must not overwrite the physical record")
	}

	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{Role: "assistant", Content: "future tail"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := LoadProviderHistorySnapshot(dir, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HeadSeq != 6 || snapshot.Checkpoint == nil || snapshot.Checkpoint.Version != 1 {
		t.Fatalf("snapshot metadata = %+v, want head=6 checkpoint=1", snapshot)
	}
	wantSnapshotContents := []string{"inline modified user", "new compact summary", "concurrent tail", "future tail"}
	if len(snapshot.Records) != len(wantSnapshotContents) {
		t.Fatalf("provider snapshot = %+v", snapshot.Records)
	}
	for i, want := range wantSnapshotContents {
		if snapshot.Records[i].Content != want {
			t.Fatalf("provider snapshot[%d] = %+v, want content=%q", i, snapshot.Records[i], want)
		}
	}
	if !reflect.DeepEqual(snapshot.Records[:2], wantPrefix) {
		t.Fatalf("loaded exact replacement prefix = %+v, want %+v", snapshot.Records[:2], wantPrefix)
	}
}

func TestStoreHistoryCheckpointSupportsConsecutiveReplacements(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	for _, rec := range []HistoryRecord{
		{Role: "user", Content: "original request"},
		{Role: "assistant", Content: "original answer"},
	} {
		if err := AppendHistoryRecord(dir, "thread-1", rec); err != nil {
			t.Fatal(err)
		}
	}
	first, err := StoreHistoryCheckpointAtBaseline(dir, "thread-1", "automatic", []HistoryRecord{
		{Seq: 1, Role: "user", Content: "first inline form"},
		{Role: "assistant", Content: "first synthetic summary"},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.ThroughSeq != 3 || first.Replacement[1].Seq != 3 {
		t.Fatalf("first checkpoint = %+v", first)
	}
	secondInput := []HistoryRecord{
		{Seq: 1, Role: "user", Content: "second inline form", FinishReason: "exact-inline"},
		{Seq: 3, Role: "assistant", Content: "modified first summary", StopReason: "exact-retained"},
		{Role: "assistant", Content: "second synthetic summary", Truncated: true},
	}
	second, err := StoreHistoryCheckpointAtBaseline(dir, "thread-1", "manual", secondInput, first.ThroughSeq)
	if err != nil {
		t.Fatal(err)
	}
	secondInput[2].Seq = 4
	if second.Version != 2 || second.Kind != "manual" || second.ThroughSeq != 4 {
		t.Fatalf("second checkpoint metadata = %+v", second)
	}
	if !reflect.DeepEqual(second.Replacement, secondInput) {
		t.Fatalf("second replacement = %+v, want exact %+v", second.Replacement, secondInput)
	}

	raw, err := LoadHistoryRecords(dir, "thread-1", true)
	if err != nil {
		t.Fatal(err)
	}
	wantRaw := []string{"original request", "original answer", "first synthetic summary", "second synthetic summary"}
	if len(raw) != len(wantRaw) {
		t.Fatalf("raw history = %+v", raw)
	}
	for i, want := range wantRaw {
		if raw[i].Seq != i+1 || raw[i].Content != want {
			t.Fatalf("raw[%d] = %+v, want seq=%d content=%q", i, raw[i], i+1, want)
		}
	}

	latest, ok, err := LatestHistoryCheckpoint(dir, "thread-1")
	if err != nil || !ok {
		t.Fatalf("LatestHistoryCheckpoint() = ok=%v err=%v", ok, err)
	}
	if latest.Version != 2 || !reflect.DeepEqual(latest.Replacement, secondInput) {
		t.Fatalf("latest checkpoint = %+v, want second replacement", latest)
	}
	snapshot, err := LoadProviderHistorySnapshot(dir, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HeadSeq != 4 || !reflect.DeepEqual(snapshot.Records, secondInput) {
		t.Fatalf("provider snapshot = %+v, want %+v", snapshot, secondInput)
	}
}

func TestHistoryCheckpointSerializesCrossProcessTailAppends(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-1", "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-1", HistoryRecord{Role: "user", Content: "baseline"}); err != nil {
		t.Fatal(err)
	}
	gatePath := filepath.Join(dir, "append-gate")
	type childProcess struct {
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	children := make([]childProcess, 2)
	t.Cleanup(func() {
		for i := range children {
			if children[i].cmd != nil && children[i].cmd.Process != nil && children[i].cmd.ProcessState == nil {
				_ = children[i].cmd.Process.Kill()
				_ = children[i].cmd.Wait()
			}
		}
	})
	readyPaths := make([]string, len(children))
	for i, content := range []string{"tail-a", "tail-b"} {
		readyPaths[i] = filepath.Join(dir, "append-ready-"+content)
		children[i].cmd = exec.Command(os.Args[0], "-test.run=^TestHistoryCheckpointCrossProcessAppendHelper$")
		children[i].cmd.Env = append(
			os.Environ(),
			"WUU_HISTORY_CHECKPOINT_APPEND_HELPER=1",
			"WUU_HISTORY_CHECKPOINT_DIR="+dir,
			"WUU_HISTORY_CHECKPOINT_THREAD=thread-1",
			"WUU_HISTORY_CHECKPOINT_CONTENT="+content,
			"WUU_HISTORY_CHECKPOINT_GATE="+gatePath,
			"WUU_HISTORY_CHECKPOINT_READY="+readyPaths[i],
		)
		children[i].cmd.Stdout = &children[i].output
		children[i].cmd.Stderr = &children[i].output
		if err := children[i].cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, readyPath := range readyPaths {
		waitForHistoryCheckpointTestPath(t, readyPath)
	}
	if err := os.WriteFile(gatePath, []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := StoreHistoryCheckpointAtBaseline(dir, "thread-1", "race", []HistoryRecord{
		{Seq: 1, Role: "user", Content: "rewritten baseline"},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range children {
		if err := children[i].cmd.Wait(); err != nil {
			t.Fatalf("append worker %d: %v\n%s", i, err, children[i].output.String())
		}
	}

	snapshot, err := LoadProviderHistorySnapshot(dir, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HeadSeq != 3 || snapshot.Checkpoint == nil || snapshot.Checkpoint.Version != checkpoint.Version {
		t.Fatalf("snapshot metadata = %+v, checkpoint=%+v", snapshot, checkpoint)
	}
	counts := map[string]int{}
	for _, rec := range snapshot.Records {
		counts[rec.Content]++
	}
	for _, content := range []string{"rewritten baseline", "tail-a", "tail-b"} {
		if counts[content] != 1 {
			t.Fatalf("provider history counts = %+v, want %q exactly once; records=%+v", counts, content, snapshot.Records)
		}
	}
	if len(snapshot.Records) != 3 {
		t.Fatalf("provider history = %+v, want replacement and two tails", snapshot.Records)
	}
	raw, err := LoadHistoryRecords(dir, "thread-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 {
		t.Fatalf("raw history = %+v, want three physical rows", raw)
	}
	for i := range raw {
		if raw[i].Seq != i+1 {
			t.Fatalf("raw history seqs = %+v, want stable monotonic addresses", raw)
		}
	}
}

func TestHistoryCheckpointCrossProcessAppendHelper(t *testing.T) {
	if os.Getenv("WUU_HISTORY_CHECKPOINT_APPEND_HELPER") == "" {
		t.Skip("subprocess helper")
	}
	if readyPath := os.Getenv("WUU_HISTORY_CHECKPOINT_READY"); readyPath != "" {
		if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gatePath := os.Getenv("WUU_HISTORY_CHECKPOINT_GATE")
	waitForHistoryCheckpointTestPath(t, gatePath)
	if _, err := AppendHistoryRecordReturningSeq(
		os.Getenv("WUU_HISTORY_CHECKPOINT_DIR"),
		os.Getenv("WUU_HISTORY_CHECKPOINT_THREAD"),
		HistoryRecord{Role: "assistant", Content: os.Getenv("WUU_HISTORY_CHECKPOINT_CONTENT")},
	); err != nil {
		t.Fatal(err)
	}
}

func waitForHistoryCheckpointTestPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for checkpoint test path %q", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Editing is a branch mutation, unlike a provider-only context reset. Its
// retraction must commit with the checkpoint and survive subsequent resets.
func TestHistoryEditKeepsAuditAndCompactedPrefix(t *testing.T) {
	dir := t.TempDir()
	const id = "edited-history"
	if _, err := CreateWithMetadata(dir, id, "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"kept prompt", "kept answer", "obsolete prompt", "obsolete answer", "concurrent tail"} {
		if err := AppendHistoryRecord(dir, id, HistoryRecord{Role: "user", Content: text}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := LoadHistoryRecords(dir, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := RewriteHistoryRecordsForEdit(dir, id, nil, 4, 6); err == nil {
		t.Fatal("invalid baseline succeeded")
	}
	active, err := LoadActiveHistoryRecords(dir, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(active, raw) {
		t.Fatal("failed edit changed active branch")
	}
	if err := RewriteHistoryRecordsForEdit(dir, id, nil, 3, 4); err != nil {
		t.Fatal(err)
	}
	if err := RewriteHistoryRecordsAtBaseline(dir, id, raw[4:], 5); err != nil {
		t.Fatal(err)
	}
	active, err = LoadActiveHistoryRecords(dir, id, true)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]HistoryRecord(nil), raw[:2]...), raw[4:]...)
	if !reflect.DeepEqual(active, want) {
		t.Fatalf("active branch = %+v, want %+v", active, want)
	}
	snapshot, err := LoadProviderHistorySnapshot(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Records, raw[4:]) {
		t.Fatalf("provider tail = %+v", snapshot.Records)
	}
	audit, err := LoadHistoryRecords(dir, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(audit, raw) {
		t.Fatal("edit changed audit transcript")
	}
}

// Legacy replacement rewrites physical sequence addresses. Retraction ranges
// from the previous transcript must not hide unrelated replacement records.
func TestHistoryReplacementClearsPreviousBranchRetractions(t *testing.T) {
	dir := t.TempDir()
	const id = "replaced-history"
	if _, err := CreateWithMetadata(dir, id, "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, id, HistoryRecord{Role: "user", Content: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := RewriteHistoryRecordsForEdit(dir, id, nil, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := RewriteHistoryRecords(dir, id, []HistoryRecord{{Role: "user", Content: "replacement"}}); err != nil {
		t.Fatal(err)
	}
	active, err := LoadActiveHistoryRecords(dir, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Content != "replacement" {
		t.Fatalf("replacement hidden by old branch: %+v", active)
	}
}
