package document

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPendingJournalReconcilesWithoutRepeatingMutation(t *testing.T) {
	for _, kind := range []string{"write", "create", "rename"} {
		t.Run(kind, func(t *testing.T) {
			state, root := t.TempDir(), t.TempDir()
			ref := Ref{Authority: "local", Root: root, Path: "note.md"}
			ctx := context.Background()
			svc := New(state, func(context.Context, Ref, bool) (Grant, error) { return Grant{Root: root, ActorID: "owner"}, nil })
			m := mutation{Kind: kind, Ref: ref, OperationID: "crash", Text: "new"}
			if kind != "create" {
				os.WriteFile(filepath.Join(root, "note.md"), []byte("old"), 0600)
				m.BaseRevision = digest([]byte("old"))
			}
			if kind == "rename" {
				m.Text = ""
				m.NewPath = "moved.md"
				if err := os.Link(filepath.Join(root, "note.md"), filepath.Join(root, "moved.md")); err != nil {
					t.Fatal(err)
				}
			} else {
				os.WriteFile(filepath.Join(root, "note.md"), []byte("new"), 0600)
			}
			data, _ := json.Marshal(m)
			receipt := filepath.Join(state, "documents", digest([]byte("owner\x00crash"))+".json")
			if err := saveJournal(receipt, journal{PayloadHash: digest(data), Mutation: m}); err != nil {
				t.Fatal(err)
			}
			result, err := svc.mutate(ctx, m)
			if err != nil || result.Status != "saved" {
				t.Fatalf("recovery: %+v %v", result, err)
			}
			if kind == "rename" {
				if _, err := os.Stat(filepath.Join(root, "note.md")); !os.IsNotExist(err) {
					t.Fatal("interrupted rename not completed")
				}
			}
			again, err := svc.mutate(ctx, m)
			if err != nil || again.Snapshot.Revision != result.Snapshot.Revision {
				t.Fatalf("replay: %+v %v", again, err)
			}
		})
	}
}
func TestRenameRecoveryDoesNotDeleteRecreatedSource(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "note.md"), []byte("same"), 0600)
	os.WriteFile(filepath.Join(root, "moved.md"), []byte("same"), 0600)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, _, err = reconcile(r, mutation{Kind: "rename", Ref: Ref{Authority: "local", Root: root, Path: "note.md"}, NewPath: "moved.md", BaseRevision: digest([]byte("same"))})
	if err == nil {
		t.Fatal("different inode accepted as interrupted rename")
	}
	if _, err = os.Stat(filepath.Join(root, "note.md")); err != nil {
		t.Fatal("recreated source removed")
	}
}

func TestChangedRenameSourceRollsBackOnlyItsOwnLink(t *testing.T) {
	root := t.TempDir()
	ref := Ref{Authority: "local", Root: root, Path: "source.md"}
	os.WriteFile(filepath.Join(root, ref.Path), []byte("old"), 0600)
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err = r.Link(ref.Path, "target.md"); err != nil {
		t.Fatal(err)
	}
	linked, err := r.Stat("target.md")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ref.Path), []byte("external edit"), 0600)
	result, err := finishLinkedRename(r, ref, "target.md", digest([]byte("old")), linked)
	if err != nil || result.Status != "conflict" {
		t.Fatalf("result: %+v %v", result, err)
	}
	if _, err = r.Stat("target.md"); !os.IsNotExist(err) {
		t.Fatal("orphan link remains")
	}
	source, err := os.ReadFile(filepath.Join(root, ref.Path))
	if err != nil || string(source) != "external edit" {
		t.Fatal("external source lost")
	}
}
