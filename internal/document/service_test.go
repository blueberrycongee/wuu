package document_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/document"
)

func fixture(t *testing.T) (*document.Service, string, string) {
	t.Helper()
	root := t.TempDir()
	state := t.TempDir()
	auth := func(ctx context.Context, ref document.Ref, write bool) (document.Grant, error) {
		if ref.Root != root {
			return document.Grant{}, errors.New("unauthorized root")
		}
		return document.Grant{Root: root, ActorID: "owner"}, nil
	}
	return document.New(state, auth), root, state
}
func TestDocumentCASReceiptsAndRename(t *testing.T) {
	s, root, _ := fixture(t)
	ctx := context.Background()
	ref := document.Ref{Authority: "local", Root: root, Path: "note.md"}
	made, err := s.Create(ctx, document.CreateRequest{Ref: ref, OperationID: "create", Text: "one"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Create(ctx, document.CreateRequest{Ref: ref, OperationID: "create", Text: "one"})
	if err != nil || replay.Snapshot.Revision != made.Snapshot.Revision {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = s.Create(ctx, document.CreateRequest{Ref: ref, OperationID: "create", Text: "different"}); err == nil {
		t.Fatal("operation reuse accepted")
	}
	saved, err := s.Write(ctx, document.WriteRequest{Ref: ref, BaseRevision: made.Snapshot.Revision, OperationID: "save", Text: "two"})
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := s.Write(ctx, document.WriteRequest{Ref: ref, BaseRevision: made.Snapshot.Revision, OperationID: "stale", Text: "lost"})
	if err != nil || conflict.Status != "conflict" || *conflict.Current.Text != "two" {
		t.Fatalf("conflict: %+v %v", conflict, err)
	}
	renamed, err := s.Rename(ctx, document.RenameRequest{Ref: ref, BaseRevision: saved.Snapshot.Revision, OperationID: "rename", NewPath: "moved.md"})
	if err != nil || renamed.Snapshot.Ref.Path != "moved.md" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if _, err = s.Rename(ctx, document.RenameRequest{Ref: ref, BaseRevision: saved.Snapshot.Revision, OperationID: "rename", NewPath: "moved.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "note.md")); !os.IsNotExist(err) {
		t.Fatal("old path remains")
	}
}
func TestConcurrentServicesAndRestart(t *testing.T) {
	s, root, state := fixture(t)
	ctx := context.Background()
	ref := document.Ref{Authority: "local", Root: root, Path: "note.md"}
	made, err := s.Create(ctx, document.CreateRequest{Ref: ref, OperationID: "create", Text: "base"})
	if err != nil {
		t.Fatal(err)
	}
	other := document.New(state, func(context.Context, document.Ref, bool) (document.Grant, error) {
		return document.Grant{Root: root, ActorID: "owner"}, nil
	})
	var wg sync.WaitGroup
	results := make(chan document.MutationResult, 2)
	errs := make(chan error, 2)
	for i, service := range []*document.Service{s, other} {
		wg.Add(1)
		go func(i int, service *document.Service) {
			defer wg.Done()
			r, e := service.Write(ctx, document.WriteRequest{Ref: ref, BaseRevision: made.Snapshot.Revision, OperationID: []string{"a", "b"}[i], Text: []string{"first", "second"}[i]})
			results <- r
			errs <- e
		}(i, service)
	}
	wg.Wait()
	close(results)
	close(errs)
	saved, conflicts := 0, 0
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for r := range results {
		if r.Status == "saved" {
			saved++
		} else if r.Status == "conflict" {
			conflicts++
		}
	}
	if saved != 1 || conflicts != 1 {
		t.Fatalf("saved=%d conflicts=%d", saved, conflicts)
	}
	replay, err := other.Create(ctx, document.CreateRequest{Ref: ref, OperationID: "create", Text: "base"})
	if err != nil || replay.Snapshot.Revision != made.Snapshot.Revision {
		t.Fatalf("durable replay: %+v %v", replay, err)
	}
}
func TestDocumentSecurityAndReadOnly(t *testing.T) {
	s, root, _ := fixture(t)
	ctx := context.Background()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600)
	for _, p := range []string{"../escape", "/absolute"} {
		if _, err := s.Create(ctx, document.CreateRequest{Ref: document.Ref{Authority: "local", Root: root, Path: p}, OperationID: p, Text: "bad"}); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		if _, err = s.Read(ctx, document.Ref{Authority: "local", Root: root, Path: "link/secret"}); err == nil {
			t.Fatal("symlink escape")
		}
	}
	os.WriteFile(filepath.Join(root, "binary"), []byte{0, 1, 2}, 0600)
	snap, err := s.Read(ctx, document.Ref{Authority: "local", Root: root, Path: "binary"})
	if err != nil || snap.ReadOnlyReason != "binary" || snap.Text != nil {
		t.Fatalf("binary: %+v %v", snap, err)
	}
	if _, err = s.Write(ctx, document.WriteRequest{Ref: snap.Ref, BaseRevision: snap.Revision, OperationID: "binary", Text: "overwrite"}); err == nil {
		t.Fatal("binary overwritten")
	}
	if _, err = s.Read(ctx, document.Ref{Authority: "local", Root: outside, Path: "secret"}); err == nil {
		t.Fatal("unauthorized root")
	}
	if _, err = s.Read(ctx, document.Ref{Authority: "lark", Root: root, Path: "secret"}); err == nil {
		t.Fatal("local service accepted Lark")
	}
}

func TestRenameAuthorizesDestination(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, "private"), 0700)
	os.WriteFile(filepath.Join(root, "note.md"), []byte("draft"), 0600)
	s := document.New(state, func(_ context.Context, ref document.Ref, _ bool) (document.Grant, error) {
		if ref.Path == "private/other.md" {
			return document.Grant{}, errors.New("destination denied")
		}
		return document.Grant{Root: root, ActorID: "owner"}, nil
	})
	ref := document.Ref{Authority: "local", Root: root, Path: "note.md"}
	snap, err := s.Read(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Rename(context.Background(), document.RenameRequest{Ref: ref, BaseRevision: snap.Revision, OperationID: "forbidden", NewPath: "private/other.md"}); err == nil {
		t.Fatal("unauthorized destination accepted")
	}
	if _, err = os.Stat(filepath.Join(root, "note.md")); err != nil {
		t.Fatal("source changed on denied rename")
	}
}

func TestWritePreservesExistingPermissionsAndLargeFilesStayReadOnly(t *testing.T) {
	s, root, _ := fixture(t)
	ctx := context.Background()
	ref := document.Ref{Authority: "local", Root: root, Path: "script.sh"}
	os.WriteFile(filepath.Join(root, ref.Path), []byte("old"), 0700)
	snap, err := s.Read(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Write(ctx, document.WriteRequest{Ref: ref, BaseRevision: snap.Revision, OperationID: "mode", Text: "new"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, ref.Path))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("permissions changed to %o", info.Mode().Perm())
	}
	huge, err := os.Create(filepath.Join(root, "huge"))
	if err != nil {
		t.Fatal(err)
	}
	if err = huge.Truncate(1 << 40); err != nil {
		huge.Close()
		t.Skip(err)
	}
	huge.Close()
	read, err := s.Read(ctx, document.Ref{Authority: "local", Root: root, Path: "huge"})
	if err != nil || read.ReadOnlyReason != "too_large" || read.Text != nil {
		t.Fatalf("large snapshot: %+v %v", read, err)
	}
}
