package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	wuuruntime "github.com/blueberrycongee/wuu/internal/runtime"
)

// Failure cases: injection must not mask committed receipts or stale revisions,
// apply part of a batch, confuse another thread with the target, or lose the
// shared SQLite CAS when two independently selected provider processes write.
func TestFixtureProcess(t *testing.T) {
	i := slices.Index(os.Args, "--")
	if i < 0 {
		return
	}
	if err := run(os.Args[i+1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type selectedClient struct {
	persistence.Documents
	exporter *persistence.Client
}

func (c *selectedClient) Export(ctx context.Context) (persistence.Snapshot, error) {
	return c.exporter.Export(ctx)
}

func fixture(t *testing.T) (string, func() *selectedClient) {
	t.Helper()
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("WUU_HOME", home)
	if err := os.WriteFile(filepath.Join(home, markerName), []byte(markerContent), 0600); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"agent":{"max_steps":17},"composition":{"version":2,"modules":["wuu.session-state","wuu.state.sqlite"]}}`)
	if err := os.WriteFile(filepath.Join(home, "config.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := install(home, os.Args[0], []string{"-test.run=^TestFixtureProcess$", "--"}); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(saved, &fields); err != nil {
		t.Fatal(err)
	}
	var keep map[string]int
	if err := json.Unmarshal(fields["agent"], &keep); err != nil || keep["max_steps"] != 17 {
		t.Fatalf("install lost unrelated configuration: %s %v", saved, err)
	}
	open := func() *selectedClient {
		t.Helper()
		var cfg config.Config
		raw, err := os.ReadFile(filepath.Join(home, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		state, err := wuuruntime.OpenState(context.Background(), wuuruntime.StateOptions{RootDir: root, HomeDir: home, Config: cfg})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := state.Close(); err != nil {
				t.Error(err)
			}
		})
		exporter := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := state.Host.CallService(ctx, "wuu.session-state", persistence.ServiceName, method, raw)
			if err != nil {
				return nil, err
			}
			return result, nil
		})
		return &selectedClient{Documents: state.Documents, exporter: exporter}
	}
	return home, open
}

func put(t *testing.T, client *selectedClient, id string, writes ...persistence.Write) persistence.Transaction {
	t.Helper()
	view, err := client.Read(context.Background(), persistence.Query{Namespace: "fixture.check", Keys: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	tx := persistence.Transaction{ID: id, ExpectedRevision: view.Revision, Writes: writes}
	if _, err := client.Commit(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	return tx
}
func control(t *testing.T, home string, rule faultRule) {
	t.Helper()
	raw, err := json.Marshal(faultControl{Version: 1, Faults: []faultRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, fixtureDir, "faults.json")
	if err := os.WriteFile(path+".next", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
}
func export(t *testing.T, client *selectedClient) persistence.Snapshot {
	t.Helper()
	s, err := client.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSelectedProviderRejectsBeforeCommitAndKeepsReceipts(t *testing.T) {
	prefix := base64.RawURLEncoding.EncodeToString([]byte("target")) + "/"
	cases := []struct {
		name, namespace, key, prior, next string
		rule                              faultRule
	}{
		{"edit-binding", "wuu.sessions.v1", "target", `{"id":"target","engine_ref":"native-before"}`, `{"id":"target","engine_ref":"native-after"}`, faultRule{Kind: "edit-binding", SessionID: "target"}},
		{"fork-create", "wuu.sessions.v1", "new", "", `{"id":"new"}`, faultRule{Kind: "fork-create"}},
		{"native-boundary", "wuu.session_messages.v1", prefix + "00000000000000000001", `{"role":"user","content":"x"}`, `{"role":"user","content":"x","engine_turn_id":"native-turn"}`, faultRule{Kind: "native-boundary", SessionID: "target"}},
		{"ack-history", "wuu.session_messages.v1", prefix + "00000000000000000002", "", `{"role":"user","client_id":"native-save-fault"}`, faultRule{Kind: "ack-history", ClientID: "native-save-fault"}},
		{"claim", "wuu.held_user_work.v1", prefix + "native-claim-fault", `{"SessionID":"target","ID":"native-claim-fault","State":"pending"}`, `{"SessionID":"target","ID":"native-claim-fault","State":"dispatching"}`, faultRule{Kind: "claim", ID: "native-claim-fault"}},
		{"release", "wuu.held_user_work.v1", prefix + "native-release-fault", `{"SessionID":"target","ID":"native-release-fault","State":"dispatching"}`, `{"SessionID":"target","ID":"native-release-fault","State":"pending"}`, faultRule{Kind: "release", ID: "native-release-fault"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, open := fixture(t)
			a, b := open(), open()
			ctx := context.Background()
			control(t, home, tc.rule)
			if tc.name == "release" {
				put(t, a, "accept", persistence.Write{Namespace: tc.namespace, Key: tc.key, Value: json.RawMessage(`{"SessionID":"target","ID":"native-release-fault","State":"pending"}`)})
			}
			if tc.prior != "" {
				put(t, a, "seed", persistence.Write{Namespace: tc.namespace, Key: tc.key, Value: json.RawMessage(tc.prior)})
			}
			before := export(t, a)
			tx := persistence.Transaction{ID: "target-commit", ExpectedRevision: before.Revision, Writes: []persistence.Write{
				{Namespace: tc.namespace, Key: tc.key, Value: json.RawMessage(tc.next)},
				{Namespace: "fixture.check", Key: "atomic-companion", Value: json.RawMessage(`true`)},
			}}
			for attempt := 0; attempt < 2; attempt++ {
				_, err := b.Commit(ctx, tx)
				var rejection *pluginhost.HostServiceError
				if !errors.As(err, &rejection) || rejection.Code != "invalid_request" || !errors.Is(err, persistence.ErrInvalid) {
					t.Fatalf("expected explicit rejection, got %T %v; service=%#v invalid=%t", err, err, rejection, errors.Is(err, persistence.ErrInvalid))
				}
				if after := export(t, a); !reflect.DeepEqual(after, before) {
					t.Fatal("rejection changed documents, revision or receipts")
				}
			}
			evidence, err := os.ReadFile(filepath.Join(home, fixtureDir, "rejections.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(evidence, []byte("\n")) != 2 {
				t.Fatalf("missing repeated rejection evidence: %s", evidence)
			}
			if err := os.Remove(filepath.Join(home, fixtureDir, "faults.json")); err != nil {
				t.Fatal(err)
			}
			receipt, err := a.Commit(ctx, tx)
			if err != nil {
				t.Fatal(err)
			}
			control(t, home, tc.rule)
			repeated, err := b.Commit(ctx, tx)
			if err != nil || repeated != receipt {
				t.Fatalf("armed fault masked committed receipt: %+v %v", repeated, err)
			}
			changed := tx
			changed.Writes = append([]persistence.Write(nil), tx.Writes...)
			changed.Writes[1].Value = json.RawMessage(`false`)
			if _, err := b.Commit(ctx, changed); !errors.Is(err, persistence.ErrOperationConflict) {
				t.Fatalf("intent conflict masked: %v", err)
			}
			stale := tx
			stale.ID = "stale"
			if _, err := b.Commit(ctx, stale); !errors.Is(err, persistence.ErrConflict) {
				t.Fatalf("revision conflict masked: %v", err)
			}
			if final := export(t, b); final.Revision != receipt.Revision || len(final.Receipts) != len(before.Receipts)+1 {
				t.Fatalf("unexpected additional commit: %+v", final.Receipts)
			}
			restored := open()
			restarted := export(t, restored)
			again, err := restored.Commit(ctx, tx)
			if err != nil || again != receipt {
				t.Fatalf("restart lost committed receipt: %+v %v", again, err)
			}
			if final := export(t, restored); !reflect.DeepEqual(final, restarted) {
				t.Fatal("receipt retry after restart changed documents, revision or receipts")
			}
		})
	}
}

func TestSelectedProviderSharesCrossProcessCASAndMatchesOnlyTarget(t *testing.T) {
	home, open := fixture(t)
	a, b := open(), open()
	ctx := context.Background()
	put(t, a, "seed", persistence.Write{Namespace: "wuu.sessions.v1", Key: "target", Value: json.RawMessage(`{"id":"target","engine_ref":"before"}`)})
	control(t, home, faultRule{Kind: "edit-binding", SessionID: "target"})
	put(t, b, "unrelated", persistence.Write{Namespace: "wuu.sessions.v1", Key: "other", Value: json.RawMessage(`{"id":"other","engine_ref":"after"}`)})
	put(t, a, "same-ref", persistence.Write{Namespace: "wuu.sessions.v1", Key: "target", Value: json.RawMessage(`{"id":"target","engine_ref":"before","title":"updated"}`)})
	before := export(t, a)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, client := range []*selectedClient{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := client.Commit(ctx, persistence.Transaction{ID: fmt.Sprintf("racer-%d", i), ExpectedRevision: before.Revision, Writes: []persistence.Write{{Namespace: "fixture.check", Key: fmt.Sprint(i), Value: json.RawMessage(`true`)}}})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, persistence.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("cross-process CAS: successes=%d conflicts=%d", success, conflict)
	}
	if got := export(t, b); got.Revision != before.Revision+1 {
		t.Fatal("shared authority has wrong revision")
	}
}

func TestFixtureRequiresSyntheticMarker(t *testing.T) {
	home := t.TempDir()
	if _, err := install(home, os.Args[0], nil); err == nil {
		t.Fatal("ordinary home accepted")
	}
	if err := run([]string{"serve", "--home", home}, bytes.NewReader(nil), &bytes.Buffer{}); err == nil {
		t.Fatal("ordinary backend accepted")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("ordinary home changed: %v %v", entries, err)
	}
}
