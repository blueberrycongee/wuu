package session

import "testing"

func TestSessionOrganizationRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "organized-thread", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder, err := CreateFolder(dir, "Topic")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateOrganization(dir, sess.ID, &folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FolderID != folder.ID {
		t.Fatalf("organization not persisted: %+v", updated)
	}
	found, ok, err := Find(dir, sess.ID)
	if err != nil || !ok {
		t.Fatalf("find organized session: ok=%v err=%v", ok, err)
	}
	if found.FolderID != folder.ID {
		t.Fatalf("organization did not round-trip: %+v", found)
	}
	empty := ""
	updated, err = UpdateOrganization(dir, sess.ID, &empty)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FolderID != "" {
		t.Fatalf("folder was not cleared: %+v", updated)
	}
	updated, err = UpdateOrganization(dir, sess.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FolderID != "" {
		t.Fatalf("nil folder update changed folder: %+v", updated)
	}
	organization, err := ListOrganization(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(organization.Folders) != 1 {
		t.Fatalf("unexpected organization: %+v", organization)
	}
}

func TestOrganizationDeletionKeepsSessions(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "organized-thread", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder, _ := CreateFolder(dir, "Topic")
	if _, err := UpdateOrganization(dir, sess.ID, &folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteFolder(dir, folder.ID); err != nil {
		t.Fatal(err)
	}
	found, ok, err := Find(dir, sess.ID)
	if err != nil || !ok {
		t.Fatalf("session was deleted with its folder: ok=%v err=%v", ok, err)
	}
	if found.FolderID != "" {
		t.Fatalf("unexpected deletion result: %+v", found)
	}
}

func TestPinSetsTimestampAndArchiveClearsIt(t *testing.T) {
	dir := t.TempDir()
	sess, err := CreateWithMetadata(dir, "pin-thread", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := UpdatePinned(dir, sess.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.PinnedAt == nil {
		t.Fatalf("pin did not set pinned_at: %+v", pinned)
	}
	unpinned, err := UpdatePinned(dir, sess.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if unpinned.PinnedAt != nil {
		t.Fatalf("unpin retained pinned_at: %+v", unpinned)
	}
	if _, err := UpdatePinned(dir, sess.ID, true); err != nil {
		t.Fatal(err)
	}
	archived, err := UpdateArchived(dir, sess.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if archived.PinnedAt != nil {
		t.Fatalf("archive retained pin: %+v", archived)
	}
}

func TestOrganizationGroupsCanBeReordered(t *testing.T) {
	dir := t.TempDir()
	firstFolder, _ := CreateFolder(dir, "First")
	secondFolder, _ := CreateFolder(dir, "Second")
	if err := ReorderFolders(dir, []string{secondFolder.ID, firstFolder.ID}); err != nil {
		t.Fatal(err)
	}
	organization, err := ListOrganization(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(organization.Folders) != 2 || organization.Folders[0].ID != secondFolder.ID || organization.Folders[1].ID != firstFolder.ID {
		t.Fatalf("folders were not reordered: %+v", organization.Folders)
	}
	if err := ReorderFolders(dir, []string{firstFolder.ID}); err == nil {
		t.Fatal("expected incomplete reorder to fail")
	}
}
