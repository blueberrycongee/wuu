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
	artifactJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: `{
		"action":"add_artifact","work_id":"` + task.ID + `","artifact_kind":"diff",
		"uri":"artifact://diff","summary":"two files","workspace_revision":"git:abc"}`})
	if err != nil {
		t.Fatalf("chat_work add_artifact error = %v", err)
	}
	var artifactResult struct {
		Artifact channels.WorkArtifact `json:"artifact"`
	}
	if err := json.Unmarshal([]byte(artifactJSON), &artifactResult); err != nil || artifactResult.Artifact.URI != "artifact://diff" {
		t.Fatalf("artifact result = %s, err = %v", artifactJSON, err)
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
	if evidenceResult.Work.ChecksSummary != "go test passed" || evidenceResult.Work.ChangedFilesCount != 2 || len(evidenceResult.Work.Artifacts) != 1 {
		t.Fatalf("evidence work = %#v", evidenceResult.Work)
	}
}

func TestChatWorkLeadStartsAndPromotesMemberRunIdempotently(t *testing.T) {
	ctx := context.Background()
	service, err := channels.Open(filepath.Join(t.TempDir(), "channels"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	owner, _ := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Owner"})
	lead, _ := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Lead"})
	room, _ := service.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Work", CreatedBy: "local-user",
		Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: owner.Agent.ID}, {MemberType: channels.MemberAgent, MemberID: lead.Agent.ID}},
	})
	leadClient, _ := service.BindAgent(ctx, lead.Agent.ID)
	task, err := leadClient.CreateTask(ctx, channels.TaskCreateParams{RoomID: room.ID, Title: "Fix", OwnerID: owner.Agent.ID, VerificationRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetChatAgent(leadClient)
	startArgs := `{"action":"start_run","work_id":"` + task.ID + `","run_kind":"producer","named_agent_id":"` + owner.Agent.ID + `","request_id":"producer-1","round":1}`
	startJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: startArgs})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Run channels.WorkRun `json:"run"`
	}
	if err := json.Unmarshal([]byte(startJSON), &started); err != nil {
		t.Fatal(err)
	}
	retryJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: startArgs})
	if err != nil {
		t.Fatal(err)
	}
	var retried struct {
		Run channels.WorkRun `json:"run"`
	}
	_ = json.Unmarshal([]byte(retryJSON), &retried)
	if started.Run.ID == "" || retried.Run.ID != started.Run.ID || started.Run.NamedAgentID != owner.Agent.ID {
		t.Fatalf("start/retry = %#v / %#v", started.Run, retried.Run)
	}
	artifactJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: `{"action":"add_artifact","work_id":"` + task.ID + `","run_id":"` + started.Run.ID + `","artifact_kind":"candidate","uri":"artifact://candidate","workspace_revision":"git:abc"}`})
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Artifact channels.WorkArtifact `json:"artifact"`
	}
	_ = json.Unmarshal([]byte(artifactJSON), &artifact)
	promotedJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: `{"action":"promote_candidate","work_id":"` + task.ID + `","run_id":"` + started.Run.ID + `","artifact_ref":"` + artifact.Artifact.ID + `","request_id":"promotion-1","selection_reason":"single route","workspace_revision":"git:abc"}`})
	if err != nil {
		t.Fatal(err)
	}
	var promoted struct {
		Work channels.Work `json:"work"`
	}
	if err := json.Unmarshal([]byte(promotedJSON), &promoted); err != nil || promoted.Work.CandidateArtifactRef != artifact.Artifact.ID || promoted.Work.CandidateRevision != 1 {
		t.Fatalf("promotion = %s, %v", promotedJSON, err)
	}
}
