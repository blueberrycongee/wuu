package pluginsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestStateTransferCheckPreservesBytesAndRejectsChangedOwner(t *testing.T) {
	home := t.TempDir()
	transfer := testStateTransfer()
	seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.one": "original"})
	sourcePath, _, _ := stateDocumentPath(home, "", transfer.SourceSubjectID, ScopeUser)
	targetPath, _, _ := stateDocumentPath(home, "", transfer.TargetSubjectID, ScopeUser)
	checkUnchanged := func(wantError bool) {
		t.Helper()
		beforeSource, _ := os.ReadFile(sourcePath)
		beforeTarget, _ := os.ReadFile(targetPath)
		err := CheckStateTransfer(context.Background(), home, "", transfer)
		if (err != nil) != wantError {
			t.Fatalf("check: %v", err)
		}
		afterSource, _ := os.ReadFile(sourcePath)
		afterTarget, _ := os.ReadFile(targetPath)
		if !bytes.Equal(beforeSource, afterSource) || !bytes.Equal(beforeTarget, afterTarget) {
			t.Fatal("read-only transfer check changed ownership or data")
		}
	}
	checkUnchanged(false)
	seedState(t, home, transfer.TargetSubjectID, map[string]string{"records.one": "newer"})
	checkUnchanged(true)
	if _, err := UpdateState(home, "", transfer.TargetSubjectID, ScopeUser, func(values map[string]string) error { delete(values, "records.one"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := MoveStatePrefix(home, "", transfer); err != nil {
		t.Fatal(err)
	}
	checkUnchanged(false)
	transfer.TargetSubjectID = "plugin:dev:target"
	checkUnchanged(true)
}

// Persistence failures to protect: interrupted ownership changes; replay after
// deletion; collisions or size failures fencing usable data; rollback readers
// and writers seeing backups; concurrent writers losing committed data; ordinary
// updates dropping journals; conflicting identities, prefixes, or scope.
func TestStateTransferRecoversInterruptedCommits(t *testing.T) {
	for _, stage := range []stateTransferStage{beforeStateTransferFence, afterStateTransferFence, afterStateTransferTargetCommit} {
		t.Run(string(stage), func(t *testing.T) {
			home := t.TempDir()
			transfer := testStateTransfer()
			original := map[string]string{
				"records.active":  "  {\"revision\":42,\"text\":\"你好\"}  \n",
				"records.deleted": `{"revision":91,"deleted":true}`,
				"other.state":     "unrelated",
			}
			seedState(t, home, transfer.SourceSubjectID, original)
			seedState(t, home, transfer.TargetSubjectID, map[string]string{"other.target": "keep"})
			interrupted := errors.New("simulated process stop")
			err := moveStatePrefix(home, "", transfer, func(current stateTransferStage) error {
				if current == stage {
					return interrupted
				}
				return nil
			})
			if !errors.Is(err, interrupted) {
				t.Fatalf("interruption = %v", err)
			}
			source := loadState(t, home, transfer.SourceSubjectID)
			_, visible := source.Values["records.active"]
			if visible != (stage == beforeStateTransferFence) {
				t.Fatalf("source visibility after %s: %+v", stage, source.Values)
			}
			if raw := rawStateValues(t, home, transfer.SourceSubjectID); !reflect.DeepEqual(raw, original) {
				t.Fatalf("backup changed: %#v", raw)
			}
			target := loadState(t, home, transfer.TargetSubjectID)
			_, copied := target.Values["records.active"]
			if copied != (stage == afterStateTransferTargetCommit) {
				t.Fatalf("target after %s: %+v", stage, target.Values)
			}
			if err := MoveStatePrefix(home, "", transfer); err != nil {
				t.Fatal(err)
			}
			target = loadState(t, home, transfer.TargetSubjectID)
			for key, value := range original {
				if strings.HasPrefix(key, transfer.KeyPrefix) && target.Values[key] != value {
					t.Fatalf("raw value changed for %s: %q", key, target.Values[key])
				}
			}
			if target.Values["other.target"] != "keep" || target.Values["other.state"] != "" {
				t.Fatalf("prefix transfer leaked: %+v", target.Values)
			}
			if got := loadState(t, home, transfer.SourceSubjectID).Values; !reflect.DeepEqual(got, map[string]string{"other.state": "unrelated"}) {
				t.Fatalf("source = %+v", got)
			}
		})
	}
}

func TestStateTransferReceiptPreventsReplayAfterForget(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupted), func(t *testing.T) {
			home := t.TempDir()
			transfer := testStateTransfer()
			seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.deleted": "old", "records.changed": "old"})
			err := moveStatePrefix(home, "", transfer, func(stage stateTransferStage) error {
				if interrupted && stage == afterStateTransferTargetCommit {
					return errors.New("stop")
				}
				return nil
			})
			if !interrupted && err != nil {
				t.Fatal(err)
			}
			if _, err := UpdateState(home, "", transfer.TargetSubjectID, ScopeUser, func(values map[string]string) error {
				delete(values, "records.deleted")
				values["records.changed"] = "new"
				values["other.target"] = "preserve receipt"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := UpdateState(home, "", transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
				if len(values) != 0 {
					t.Fatalf("callback saw backup: %+v", values)
				}
				values["other.source"] = "preserve fence"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := MoveStatePrefix(home, "", transfer); err != nil {
					t.Fatal(err)
				}
			}
			target := loadState(t, home, transfer.TargetSubjectID)
			if _, exists := target.Values["records.deleted"]; exists {
				t.Fatal("forgotten value resurrected")
			}
			if target.Values["records.changed"] != "new" {
				t.Fatalf("updated target overwritten: %+v", target.Values)
			}
			if got := loadState(t, home, transfer.SourceSubjectID); len(got.Values) != 1 || got.Values["other.source"] == "" {
				t.Fatalf("fence lost: %+v", got.Values)
			}
			if got := rawStateValues(t, home, transfer.SourceSubjectID); got["records.deleted"] != "old" {
				t.Fatal("source backup dropped by unrelated update")
			}
		})
	}
}

func TestStateTransferPreflightDoesNotFenceOnCollisionOrLimit(t *testing.T) {
	for _, name := range []string{"same key", "different key same prefix", "entry limit", "document limit"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			transfer := testStateTransfer()
			source := map[string]string{"records.source": "old"}
			target := map[string]string{}
			switch name {
			case "same key":
				target["records.source"] = "old"
			case "different key same prefix":
				target["records.target"] = "other"
			case "entry limit":
				for i := range maxStateEntries {
					target[fmt.Sprintf("other.%d", i)] = "x"
				}
			case "document limit":
				source["records.source"] = strings.Repeat("a", MaxStateValueBytes)
				target["other.large"] = strings.Repeat("b", MaxStateValueBytes)
			}
			seedState(t, home, transfer.SourceSubjectID, source)
			seedState(t, home, transfer.TargetSubjectID, target)
			if err := MoveStatePrefix(home, "", transfer); err == nil {
				t.Fatal("conflicting transfer succeeded")
			}
			if got := loadState(t, home, transfer.SourceSubjectID).Values; !reflect.DeepEqual(got, source) {
				t.Fatalf("failed preflight fenced source: %+v", got)
			}
			if got := loadState(t, home, transfer.TargetSubjectID).Values; !reflect.DeepEqual(got, target) {
				t.Fatal("failed preflight modified target")
			}
		})
	}
}

func TestStateTransferFenceBlocksRollbackAndCASWrites(t *testing.T) {
	home := t.TempDir()
	transfer := testStateTransfer()
	seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.secret": "backup", "other.state": "keep"})
	if err := MoveStatePrefix(home, "", transfer); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"records.secret", "records.new"} {
		if _, err := UpdateState(home, "", transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
			if _, exists := values["records.secret"]; exists {
				t.Fatal("CAS callback read backup")
			}
			// A stale or expected-absent compare/exchange cannot create a second owner.
			values[key] = "rollback write"
			return nil
		}); err == nil {
			t.Fatalf("fenced write to %s succeeded", key)
		}
	}
	var callbackValues map[string]string
	if _, err := UpdateState(home, "", transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
		callbackValues = values
		delete(values, "records.secret") // Already absent from the callback's view.
		values["other.state"] = "updated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists := callbackValues["records.secret"]; exists {
		t.Fatal("retained callback map exposed backup after update")
	}
	if got := rawStateValues(t, home, transfer.SourceSubjectID); got["records.secret"] != "backup" || got["other.state"] != "updated" {
		t.Fatalf("backup lost: %+v", got)
	}
	if got := loadState(t, home, transfer.SourceSubjectID); len(got.Values) != 1 {
		t.Fatalf("rollback exposed old source: %+v", got.Values)
	}
}

func TestStateTransferSerializesConcurrentOldWriters(t *testing.T) {
	t.Run("writer first", func(t *testing.T) {
		home := t.TempDir()
		transfer := testStateTransfer()
		seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "old"})
		entered, release := make(chan struct{}), make(chan struct{})
		writerDone, transferDone := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := UpdateState(home, "", transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
				close(entered)
				<-release
				values["records.value"] = "latest committed"
				return nil
			})
			writerDone <- err
		}()
		<-entered
		started := make(chan struct{})
		go func() { close(started); transferDone <- MoveStatePrefix(home, "", transfer) }()
		<-started
		close(release)
		if err := <-writerDone; err != nil {
			t.Fatal(err)
		}
		if err := <-transferDone; err != nil {
			t.Fatal(err)
		}
		if got := loadState(t, home, transfer.TargetSubjectID).Values["records.value"]; got != "latest committed" {
			t.Fatalf("lost concurrent write: %q", got)
		}
	})
	t.Run("transfer first", func(t *testing.T) {
		home := t.TempDir()
		transfer := testStateTransfer()
		seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "old"})
		fenced, release := make(chan struct{}), make(chan struct{})
		transferDone, writerDone := make(chan error, 1), make(chan error, 1)
		go func() {
			transferDone <- moveStatePrefix(home, "", transfer, func(stage stateTransferStage) error {
				if stage == afterStateTransferFence {
					close(fenced)
					<-release
				}
				return nil
			})
		}()
		<-fenced
		if got := loadState(t, home, transfer.SourceSubjectID).Values; len(got) != 0 {
			t.Fatalf("pending fence leaked: %+v", got)
		}
		started := make(chan struct{})
		go func() {
			close(started)
			_, err := UpdateState(home, "", transfer.SourceSubjectID, ScopeUser, func(values map[string]string) error {
				if len(values) != 0 {
					return errors.New("writer observed backup")
				}
				values["records.value"] = "stale"
				return nil
			})
			writerDone <- err
		}()
		<-started
		close(release)
		if err := <-transferDone; err != nil {
			t.Fatal(err)
		}
		if err := <-writerDone; err == nil || strings.Contains(err.Error(), "observed backup") {
			t.Fatalf("old writer result = %v", err)
		}
		if got := loadState(t, home, transfer.TargetSubjectID).Values["records.value"]; got != "old" {
			t.Fatalf("stale writer changed target: %q", got)
		}
	})
}

func TestStateTransferRejectsConflictingJournalsAndInvalidScope(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(*StateTransfer)
	}{
		{"different target", func(v *StateTransfer) { v.TargetSubjectID = "plugin:another-target" }},
		{"different source", func(v *StateTransfer) { v.SourceSubjectID = "plugin:another-source" }},
		{"reused id", func(v *StateTransfer) { v.KeyPrefix = "other." }},
		{"overlapping prefix", func(v *StateTransfer) { v.ID = "another-transfer"; v.KeyPrefix = "records.narrow." }},
		{"overlapping parent", func(v *StateTransfer) { v.ID = "another-transfer"; v.KeyPrefix = "record" }},
		{"workspace", func(v *StateTransfer) { v.Scope = ScopeWorkspace }},
		{"invalid scope", func(v *StateTransfer) { v.Scope = "invalid" }},
		{"same subject", func(v *StateTransfer) { v.TargetSubjectID = v.SourceSubjectID }},
		{"empty prefix", func(v *StateTransfer) { v.KeyPrefix = "" }},
		{"unnormalized subject", func(v *StateTransfer) { v.SourceSubjectID += " " }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			home := t.TempDir()
			transfer := testStateTransfer()
			seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "old", "other.value": "visible"})
			if err := MoveStatePrefix(home, "", transfer); err != nil {
				t.Fatal(err)
			}
			mutate.change(&transfer)
			if err := MoveStatePrefix(home, t.TempDir(), transfer); err == nil {
				t.Fatal("conflicting transfer accepted")
			}
			original := testStateTransfer()
			if got := loadState(t, home, original.SourceSubjectID).Values; len(got) != 1 || got["other.value"] != "visible" {
				t.Fatalf("conflict changed fence: %+v", got)
			}
		})
	}
}

func TestStateTransferRejectsCorruptJournalBeforeFencing(t *testing.T) {
	home := t.TempDir()
	transfer := testStateTransfer()
	seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "old"})
	seedState(t, home, transfer.TargetSubjectID, nil)
	path, _, err := stateDocumentPath(home, "", transfer.TargetSubjectID, ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["transfer_receipts"] = []map[string]any{{"id": transfer.ID, "scope": "workspace"}}
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MoveStatePrefix(home, "", transfer); err == nil {
		t.Fatal("corrupt receipt accepted")
	}
	if got := loadState(t, home, transfer.SourceSubjectID).Values["records.value"]; got != "old" {
		t.Fatalf("source fenced on corrupt target: %q", got)
	}
}

func testStateTransfer() StateTransfer {
	return StateTransfer{ID: "ownership-v1", SourceSubjectID: "plugin:source", TargetSubjectID: "plugin:target", KeyPrefix: "records.", Scope: ScopeUser}
}

func seedState(t *testing.T, home, subject string, values map[string]string) {
	t.Helper()
	if _, err := UpdateState(home, "", subject, ScopeUser, func(stored map[string]string) error {
		for key, value := range values {
			stored[key] = value
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func loadState(t *testing.T, home, subject string) StateDocument {
	t.Helper()
	document, err := ReadState(home, "", subject, ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func rawStateValues(t *testing.T, home, subject string) map[string]string {
	t.Helper()
	path, _, err := stateDocumentPath(home, "", subject, ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document StateDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document.Values
}

func TestStateTransferVersionFencesLegacyHostsWithoutUpgradingUntouchedState(t *testing.T) {
	home := t.TempDir()
	transfer := testStateTransfer()
	seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "backup"})
	seedState(t, home, transfer.TargetSubjectID, map[string]string{"other.value": "target"})
	const untouched = "plugin:unrelated"
	seedState(t, home, untouched, map[string]string{"records.value": "unchanged"})
	if err := MoveStatePrefix(home, "", transfer); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{transfer.SourceSubjectID, transfer.TargetSubjectID} {
		if err := legacyStateSchemaCheck(t, home, subject); err == nil {
			t.Fatalf("legacy host would read and rewrite transferred document %s", subject)
		}
		if _, err := UpdateState(home, "", subject, ScopeUser, func(values map[string]string) error {
			values["other.updated"] = "ordinary write"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got := loadState(t, home, subject).SchemaVersion; got != 2 {
			t.Fatalf("ordinary update downgraded transfer schema to %d", got)
		}
		if err := legacyStateSchemaCheck(t, home, subject); err == nil {
			t.Fatal("ordinary update reopened legacy host access")
		}
	}
	if err := legacyStateSchemaCheck(t, home, untouched); err != nil {
		t.Fatalf("untouched state no longer works with legacy host: %v", err)
	}
	if got := loadState(t, home, untouched); got.SchemaVersion != 1 || got.Values["records.value"] != "unchanged" {
		t.Fatalf("unrelated state upgraded or changed: %+v", got)
	}
}

func TestStateTransferRejectsVersionOneWithOwnershipMetadata(t *testing.T) {
	for _, source := range []bool{true, false} {
		t.Run(fmt.Sprint(source), func(t *testing.T) {
			home := t.TempDir()
			transfer := testStateTransfer()
			seedState(t, home, transfer.SourceSubjectID, map[string]string{"records.value": "backup"})
			if err := MoveStatePrefix(home, "", transfer); err != nil {
				t.Fatal(err)
			}
			subject := transfer.TargetSubjectID
			if source {
				subject = transfer.SourceSubjectID
			}
			path, _, err := stateDocumentPath(home, "", subject, ScopeUser)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			record["schema_version"] = 1
			data, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadState(home, "", subject, ScopeUser); err == nil {
				t.Fatal("version-one ownership metadata accepted")
			}
			if err := MoveStatePrefix(home, "", transfer); err == nil {
				t.Fatal("transfer silently repaired a downgraded ownership record")
			}
		})
	}
}

// The pre-transfer host accepts only schema 1 before any read or callback. Keep
// that compatibility boundary here rather than asking old code to understand
// metadata fields it would silently discard on its next ordinary write.
func legacyStateSchemaCheck(t *testing.T, home, subject string) error {
	t.Helper()
	path, _, err := stateDocumentPath(home, "", subject, ScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document StateDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.PluginID != subject {
		return errors.New("plugin storage ownership or schema mismatch")
	}
	return nil
}
