package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestChatWorkRecordsArtifactAndEvidence(t *testing.T) {
	ctx := context.Background()
	service, err := channels.Open(filepath.Join(t.TempDir(), "channels"), nil)
	if err != nil {
		t.Fatalf("channels.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	owner, _ := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Owner"})
	room, _ := service.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Work", CreatedBy: "local-user",
		Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: owner.Agent.ID}},
	})
	ownerClient, _ := service.BindAgent(ctx, owner.Agent.ID)
	task, err := ownerClient.CreateTask(ctx, channels.TaskCreateParams{RoomID: room.ID, Title: "Fix", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	kit.SetChatAgent(ownerClient)
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: `{"action":"start_run","work_id":"` + task.ID + `","run_kind":"producer"}`}); err == nil {
		t.Fatal("model created a host-owned execution run")
	}
	evidenceJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: `{
		"action":"evidence","work_id":"` + task.ID + `","checks_summary":"go test passed",
		"changed_files_count":2,"unresolved_items":"none"}`})
	if err != nil {
		t.Fatalf("chat_work evidence error = %v", err)
	}
	var evidenceResult struct {
		Work channels.Work `json:"work"`
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &evidenceResult); err != nil {
		t.Fatalf("decode evidence = %v", err)
	}
	if evidenceResult.Work.ChecksSummary != "go test passed" || evidenceResult.Work.ChangedFilesCount != 2 {
		t.Fatalf("evidence work = %#v", evidenceResult.Work)
	}
}
