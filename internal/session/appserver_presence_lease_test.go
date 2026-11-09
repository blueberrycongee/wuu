package session

import "testing"

func TestAppServerPresenceElectsOnlyTheFirstLiveServer(t *testing.T) {
	dir := t.TempDir()
	first, elected, err := AcquireAppServerPresence(dir)
	if err != nil {
		t.Fatalf("acquire first presence: %v", err)
	}
	if !elected {
		t.Fatal("first live app-server was not elected for boot settlement")
	}
	if err := first.FinalizeStartup(); err != nil {
		t.Fatalf("finalize first presence: %v", err)
	}
	defer first.Release()

	second, elected, err := AcquireAppServerPresence(dir)
	if err != nil {
		t.Fatalf("acquire second presence: %v", err)
	}
	if elected {
		t.Fatal("second live app-server was incorrectly elected for boot settlement")
	}
	if err := second.FinalizeStartup(); err != nil {
		t.Fatalf("finalize second presence: %v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release first presence: %v", err)
	}
	third, elected, err := AcquireAppServerPresence(dir)
	if err != nil {
		t.Fatalf("acquire third presence: %v", err)
	}
	if elected {
		t.Fatal("a remaining shared owner must prevent boot settlement")
	}
	if err := third.FinalizeStartup(); err != nil {
		t.Fatalf("finalize third presence: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release third presence: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("release second presence: %v", err)
	}

	last, elected, err := AcquireAppServerPresence(dir)
	if err != nil {
		t.Fatalf("acquire presence after all owners exited: %v", err)
	}
	if !elected {
		t.Fatal("first app-server after all owners exited must run boot settlement")
	}
	if err := last.FinalizeStartup(); err != nil {
		t.Fatalf("finalize last presence: %v", err)
	}
	if err := last.Release(); err != nil {
		t.Fatalf("release last presence: %v", err)
	}
}
