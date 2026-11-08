package session

import (
	"errors"
	"testing"
)

func TestCreateInitializedWithLaunchPersistsSeedWithoutSourceHistory(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if _, err := store.CreateWithMetadata("source", t.TempDir()); err != nil {
			t.Fatalf("create source: %v", err)
		}
		if err := store.AppendHistoryRecords("source", []HistoryRecord{
			{Role: "user", Content: "SENTINEL-SOURCE-ONLY"},
			{Role: "assistant", Content: "investigation notes"},
		}); err != nil {
			t.Fatalf("append source: %v", err)
		}
		seed := ContextSeed{
			Version: ContextSeedVersionV1,
			Body:    "Continue from the verified performance fix. Do not treat layout advice as a hard constraint.",
			Source:  HistorySnapshot{SessionID: "source", ThroughSeq: 2},
			References: []ContextSeedReference{{
				ID: "r1", History: HistoryRef{Snapshot: HistorySnapshot{SessionID: "source", ThroughSeq: 2}, StartSeq: 1, EndSeq: 1},
			}},
			Provenance: ContextSeedProvenance{Producer: "test"},
		}
		created, err := store.CreateInitializedWithLaunch(Session{
			ID: "target", CWD: t.TempDir(), Provider: "openai", Model: "gpt-test", ContextSource: "seed",
		}, nil, seed, SessionLaunchRecord{
			RequestID: "handoff-1", Revision: 1, Kind: SessionLaunchKindHandoff, SourceSession: "source", SourceCutoff: 2,
			Runtime: SessionRuntimeSelection{Provider: "openai", Model: "gpt-test"},
		})
		if err != nil {
			t.Fatalf("create target: %v", err)
		}
		if created.ContextSource != "seed" || created.SeedID == "" {
			t.Fatalf("created = %+v", created)
		}
		page, err := store.ReadHistoryPage("target", 1, 20)
		if err != nil {
			t.Fatalf("read target history: %v", err)
		}
		for _, record := range page.Records {
			if record.Content == "SENTINEL-SOURCE-ONLY" {
				t.Fatalf("target copied source history: %+v", page.Records)
			}
		}
		if len(page.Records) != 1 || page.Records[0].Name != "context_seed" || page.Records[0].Origin != "handoff" {
			t.Fatalf("target history = %+v", page.Records)
		}
		again, err := store.CreateInitializedWithLaunch(Session{ID: "other", CWD: t.TempDir()}, nil, seed, SessionLaunchRecord{
			RequestID: "handoff-1", Revision: 1, Kind: SessionLaunchKindHandoff, SourceSession: "source", SourceCutoff: 2,
			Runtime: SessionRuntimeSelection{Provider: "openai", Model: "gpt-test"},
		})
		if err != nil {
			t.Fatalf("idempotent create: %v", err)
		}
		if again.ID != created.ID {
			t.Fatalf("idempotent target = %q, want %q", again.ID, created.ID)
		}
		_, err = store.CreateInitializedWithLaunch(Session{ID: "conflict", CWD: t.TempDir()}, nil, seed, SessionLaunchRecord{
			RequestID: "handoff-1", Revision: 2, Kind: SessionLaunchKindHandoff, SourceSession: "source", SourceCutoff: 2,
			Runtime: SessionRuntimeSelection{Provider: "openai", Model: "other-model"},
		})
		if !errors.Is(err, ErrSessionLaunchConflict) {
			t.Fatalf("conflict error = %v", err)
		}
	})
}

func TestCreateInitializedWithLaunchPersistsSeedWithExternalSource(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		// A zero cutoff records a source this host never held locally; the seed
		// must persist without resolving it against local history.
		seed := ContextSeed{
			Version:    ContextSeedVersionV1,
			Body:       "Shared context imported from another Wuu host.",
			Source:     HistorySnapshot{SessionID: "remote-session", ThroughSeq: 0},
			Provenance: ContextSeedProvenance{Producer: "plugin:session-share"},
		}
		created, err := store.CreateInitializedWithLaunch(Session{
			ID: "target", CWD: t.TempDir(), Provider: "openai", Model: "gpt-test", ContextSource: "seed",
		}, nil, seed, SessionLaunchRecord{
			RequestID: "share-import-1", Revision: 1, Kind: SessionLaunchKindHandoff, SourceSession: "remote-session", SourceCutoff: 0,
			Runtime: SessionRuntimeSelection{Provider: "openai", Model: "gpt-test"},
		})
		if err != nil {
			t.Fatalf("create external-source seed: %v", err)
		}
		page, err := store.ReadHistoryPage(created.ID, 1, 20)
		if err != nil {
			t.Fatalf("read target history: %v", err)
		}
		if len(page.Records) != 1 || page.Records[0].Name != "context_seed" || page.Records[0].RelatedSessionID != "remote-session" {
			t.Fatalf("target history = %+v", page.Records)
		}
		// Deleting a local session that shares the external source id must not
		// garbage-collect provenance-only seeds.
		if _, err := store.CreateWithMetadata("remote-session", t.TempDir()); err != nil {
			t.Fatalf("create same-named session: %v", err)
		}
		if _, err := store.Delete("remote-session"); err != nil {
			t.Fatalf("delete same-named session: %v", err)
		}
		if _, found, err := store.LoadContextSeed(created.SeedID); err != nil || !found {
			t.Fatalf("external seed collected by id match: found=%v err=%v", found, err)
		}
	})
}

func TestPutContextSeedStillRequiresLocalPositiveCutoff(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		seed := ContextSeed{
			Version:    ContextSeedVersionV1,
			Body:       "brief",
			Source:     HistorySnapshot{SessionID: "ghost", ThroughSeq: 3},
			Provenance: ContextSeedProvenance{Producer: "test"},
		}
		_, err := store.CreateInitializedWithLaunch(Session{
			ID: "target", CWD: t.TempDir(), Provider: "openai", Model: "gpt-test", ContextSource: "seed",
		}, nil, seed, SessionLaunchRecord{RequestID: "seed-1", Revision: 1, Kind: SessionLaunchKindHandoff, Runtime: SessionRuntimeSelection{Provider: "openai", Model: "gpt-test"}})
		if !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("missing source error = %v", err)
		}
		if _, err := store.CreateWithMetadata("ghost", t.TempDir()); err != nil {
			t.Fatalf("create source: %v", err)
		}
		_, err = store.CreateInitializedWithLaunch(Session{
			ID: "target", CWD: t.TempDir(), Provider: "openai", Model: "gpt-test", ContextSource: "seed",
		}, nil, seed, SessionLaunchRecord{RequestID: "seed-2", Revision: 1, Kind: SessionLaunchKindHandoff, Runtime: SessionRuntimeSelection{Provider: "openai", Model: "gpt-test"}})
		if !errors.Is(err, ErrHistorySnapshotGone) {
			t.Fatalf("beyond-head error = %v", err)
		}
	})
}

func TestValidateContextSeedRejectsNegativeCutoff(t *testing.T) {
	err := ValidateContextSeed(ContextSeed{
		Version:    ContextSeedVersionV1,
		Body:       "brief",
		Source:     HistorySnapshot{SessionID: "source", ThroughSeq: -1},
		Provenance: ContextSeedProvenance{Producer: "test"},
	})
	if !errors.Is(err, ErrContextSeedInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateContextSeedRejectsOutOfRangeReferences(t *testing.T) {
	err := ValidateContextSeed(ContextSeed{
		Version: ContextSeedVersionV1,
		Body:    "brief",
		Source:  HistorySnapshot{SessionID: "source", ThroughSeq: 2},
		References: []ContextSeedReference{{
			ID: "r1", History: HistoryRef{Snapshot: HistorySnapshot{SessionID: "source", ThroughSeq: 2}, StartSeq: 1, EndSeq: 9},
		}},
		Provenance: ContextSeedProvenance{Producer: "test"},
	})
	if !errors.Is(err, ErrContextSeedInvalid) {
		t.Fatalf("error = %v", err)
	}
}
