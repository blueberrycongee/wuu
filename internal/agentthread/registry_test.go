package agentthread

import (
	"testing"
	"time"
)

func TestRegistryRegistersRootAndChildPath(t *testing.T) {
	reg := NewRegistry()
	now := time.Date(2026, 5, 23, 1, 2, 3, 0, time.UTC)
	root := reg.RegisterRoot("root-thread", "sess-1", "/repo", "gpt-5", now)
	if root.Path != RootPath || root.Source.Kind != SourceRoot {
		t.Fatalf("unexpected root metadata: %+v", root)
	}

	child, err := reg.RegisterSpawn(SpawnSpec{
		ID:              "worker-1",
		SessionID:       "sess-1",
		ParentPath:      RootPath,
		TaskName:        "fix_login_bug",
		Role:            "worker",
		LastTaskMessage: "fix it",
		Status:          StatusRunning,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("RegisterSpawn: %v", err)
	}
	if child.Path != "/root/fix_login_bug" {
		t.Fatalf("unexpected child path %q", child.Path)
	}
	if child.ParentID != "root-thread" || child.Source.ParentThreadID != "root-thread" {
		t.Fatalf("child did not link to root: %+v", child)
	}
	if got, ok := reg.Resolve("fix_login_bug"); !ok || got.ID != "worker-1" {
		t.Fatalf("resolve by task name failed: %+v ok=%v", got, ok)
	}
	if got, ok := reg.Resolve("/root/fix_login_bug"); !ok || got.ID != "worker-1" {
		t.Fatalf("resolve by path failed: %+v ok=%v", got, ok)
	}
}

func TestRegistryRejectsDuplicateTaskPath(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterRoot("root-thread", "sess-1", "", "", time.Now())
	if _, err := reg.RegisterSpawn(SpawnSpec{ID: "a", TaskName: "same_task"}); err != nil {
		t.Fatalf("first spawn should succeed: %v", err)
	}
	if _, err := reg.RegisterSpawn(SpawnSpec{ID: "b", TaskName: "same_task"}); err == nil {
		t.Fatal("expected duplicate path error")
	}
}

func TestRegistryRejectsInvalidTaskName(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterRoot("root-thread", "sess-1", "", "", time.Now())
	if _, err := reg.RegisterSpawn(SpawnSpec{ID: "a"}); err == nil {
		t.Fatal("expected empty task_name to fail")
	}
	if _, err := reg.RegisterSpawn(SpawnSpec{ID: "b", TaskName: "same task"}); err == nil {
		t.Fatal("expected natural-language task_name to fail")
	}
}

func TestRegistrySubtreeReturnsNodeAndDescendants(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterRoot("root-thread", "sess-1", "", "", time.Now())
	parent, err := reg.RegisterSpawn(SpawnSpec{ID: "parent", TaskName: "parent"})
	if err != nil {
		t.Fatalf("register parent: %v", err)
	}
	child, err := reg.RegisterSpawn(SpawnSpec{
		ID:         "child",
		ParentID:   parent.ID,
		ParentPath: parent.Path,
		TaskName:   "child",
	})
	if err != nil {
		t.Fatalf("register child: %v", err)
	}
	if _, err := reg.RegisterSpawn(SpawnSpec{ID: "sibling", TaskName: "sibling"}); err != nil {
		t.Fatalf("register sibling: %v", err)
	}

	got := reg.Subtree(parent.ID)
	if len(got) != 2 {
		t.Fatalf("expected parent subtree only, got %+v", got)
	}
	if got[0].ID != parent.ID || got[1].ID != child.ID {
		t.Fatalf("unexpected subtree order/content: %+v", got)
	}
}

func TestRegistryRestoresPersistedMetadata(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterRoot("root-thread", "sess-1", "", "", time.Now())
	meta := Metadata{
		ID:        "worker-1",
		Path:      "/root/queued_task",
		TaskName:  "queued_task",
		ParentID:  "root-thread",
		Role:      "worker",
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Source: Source{
			Kind:           SourceThreadSpawn,
			ParentThreadID: "root-thread",
			ParentPath:     RootPath,
			Depth:          2,
			EdgeStatus:     EdgeOpen,
		},
	}
	if err := reg.Restore(meta); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, ok := reg.Resolve("queued_task")
	if !ok || got.ID != meta.ID || got.Path != meta.Path {
		t.Fatalf("restored metadata did not resolve: %+v ok=%v", got, ok)
	}
	if err := reg.Restore(Metadata{ID: "worker-2", Path: meta.Path}); err == nil {
		t.Fatal("expected duplicate path restore to fail")
	}
}
