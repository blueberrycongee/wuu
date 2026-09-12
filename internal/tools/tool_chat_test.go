package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestNamedAgentChatToolsAreIsolatedAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	service, err := channels.Open(filepath.Join(t.TempDir(), "channels"), nil)
	if err != nil {
		t.Fatalf("channels.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	credential, err := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Alpha"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	room, err := service.OpenDirectMessage(ctx, "human-1", credential.Agent.ID)
	if err != nil {
		t.Fatalf("OpenDirectMessage() error = %v", err)
	}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("tools.New() error = %v", err)
	}
	profile := modelprofile.Resolve("openai", "gpt-5-codex")
	kit.SetActiveProfile(profile, true)
	assertDefinitionMissing(t, kit.Definitions(), "chat_check")
	assertDefinitionMissing(t, kit.Definitions(), "chat_draft")
	assertDefinitionMissing(t, kit.Definitions(), "chat_task")
	assertDefinitionMissing(t, kit.Definitions(), "chat_verify")
	assertDefinitionMissing(t, kit.Definitions(), "chat_remind")
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_check", Arguments: `{}`}); err == nil {
		t.Fatal("ordinary session executed chat_check")
	}

	client, err := service.BindAgent(ctx, credential.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	kit.SetChatAgent(client)
	for _, name := range []string{"chat_check", "chat_read", "chat_send", "collaboration_send", "chat_draft", "chat_task", "chat_work", "chat_remind", "chat_verify", "chat_roster"} {
		assertDefinitionPresent(t, kit.Definitions(), name)
	}
	assertDefinitionProperties(t, kit.Definitions(), "collaboration_send", "target_session_ref", "work_id")
	assertDefinitionProperties(t, kit.Definitions(), "chat_task", "target_session_ref")
	for _, definition := range kit.Definitions() {
		if definition.Name != "chat_send" {
			continue
		}
		properties, _ := definition.InputSchema["properties"].(map[string]any)
		if _, ok := properties["thread_id"]; ok {
			t.Fatal("chat_send unexpectedly exposes thread_id")
		}
		if _, ok := properties["reply_to"]; ok {
			t.Fatal("chat_send unexpectedly exposes reply_to")
		}
	}

	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "collaboration_send", Arguments: `{"room_id":"` + room.ID + `","target_kind":"room_runtime","target_id":"retired-runtime","body":"review"}`}); err == nil {
		t.Fatal("collaboration_send accepted a retired room runtime target")
	}

	sentJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_send", Arguments: `{"room_id":"` + room.ID + `","kind":"text","body":"reviewed","basis_seq":0}`})
	if err != nil {
		t.Fatalf("chat_send error = %v", err)
	}
	var sent struct {
		Status  string           `json:"status"`
		Message channels.Message `json:"message"`
	}
	if err := json.Unmarshal([]byte(sentJSON), &sent); err != nil {
		t.Fatalf("decode chat_send = %v: %s", err, sentJSON)
	}
	if sent.Status != "committed" || sent.Message.AuthorID != credential.Agent.ID || sent.Message.Seq != 1 {
		t.Fatalf("chat_send result = %#v", sent)
	}

	readJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_read", Arguments: `{"room_id":"` + room.ID + `","after_seq":0,"limit":10}`})
	if err != nil {
		t.Fatalf("chat_read error = %v", err)
	}
	var read struct {
		Messages []channels.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(readJSON), &read); err != nil {
		t.Fatalf("decode chat_read = %v: %s", err, readJSON)
	}
	if len(read.Messages) != 1 || read.Messages[0].Body != "reviewed" {
		t.Fatalf("chat_read messages = %#v", read.Messages)
	}

	if _, err := service.SendHuman(ctx, channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha follow up",
		Images: []channels.MessageImage{{MediaType: "image/png", Data: "aW1hZ2U="}},
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	checkJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_check", Arguments: `{}`})
	if err != nil {
		t.Fatalf("chat_check error = %v", err)
	}
	var check channels.CheckResult
	if err := json.Unmarshal([]byte(checkJSON), &check); err != nil {
		t.Fatalf("decode chat_check = %v: %s", err, checkJSON)
	}
	if len(check.Items) != 1 || check.Items[0].MessageID == "" {
		t.Fatalf("chat_check result = %#v", check)
	}
	richRead, err := kit.ExecuteResult(ctx, providers.ToolCall{Name: "chat_read", Arguments: `{"item_ids":["` + check.Items[0].ID + `"]}`})
	if err != nil {
		t.Fatalf("rich chat_read error = %v", err)
	}
	if len(richRead.Content) != 2 || richRead.Content[0].Type != toolresult.ContentTypeText || richRead.Content[1].Type != toolresult.ContentTypeImage {
		t.Fatalf("rich chat_read content = %#v", richRead.Content)
	}
	if richRead.Content[1].Data != "aW1hZ2U=" || strings.Contains(richRead.Content[0].Text, "aW1hZ2U=") {
		t.Fatalf("rich chat_read did not separate binary payload: %#v", richRead.Content)
	}
	if err := json.Unmarshal([]byte(richRead.Content[0].Text), &read); err != nil {
		t.Fatalf("decode rich chat_read text = %v: %s", err, richRead.Content[0].Text)
	}
	if len(read.Messages) != 1 || read.Messages[0].Body != "@Alpha follow up" || len(read.Messages[0].Images) != 1 {
		t.Fatalf("rich chat_read messages = %#v", read.Messages)
	}

	kit.SetImageInputSupported(false)
	omittedRead, err := kit.ExecuteResult(ctx, providers.ToolCall{Name: "chat_read", Arguments: `{"room_id":"` + room.ID + `","after_seq":0,"limit":10}`})
	if err != nil {
		t.Fatalf("non-vision chat_read error = %v", err)
	}
	if len(omittedRead.Content) != 2 || omittedRead.Content[0].Type != toolresult.ContentTypeText || omittedRead.Content[1].Text != "[1 image omitted: unsupported]" {
		t.Fatalf("non-vision chat_read content = %#v", omittedRead.Content)
	}
	if strings.Contains(omittedRead.Content[0].Text, "aW1hZ2U=") || strings.Contains(omittedRead.Content[0].Text, `"images"`) || omittedRead.Content[1].Type == toolresult.ContentTypeImage {
		t.Fatalf("non-vision chat_read exposed image data: %#v", omittedRead.Content)
	}

	heldJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_send", Arguments: `{"room_id":"` + room.ID + `","kind":"text","body":"stale answer","basis_seq":1}`})
	if err != nil {
		t.Fatalf("stale chat_send error = %v", err)
	}
	var held struct {
		Status string              `json:"status"`
		Draft  channels.Draft      `json:"draft"`
		Delta  channels.DraftDelta `json:"delta"`
	}
	if err := json.Unmarshal([]byte(heldJSON), &held); err != nil {
		t.Fatalf("decode held chat_send = %v: %s", err, heldJSON)
	}
	if held.Status != "held" || held.Draft.ID == "" || held.Delta.Count != 1 {
		t.Fatalf("held chat_send result = %#v", held)
	}
	listJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_draft", Arguments: `{"action":"list"}`})
	if err != nil || !strings.Contains(listJSON, held.Draft.ID) {
		t.Fatalf("chat_draft list = %s, err = %v", listJSON, err)
	}
	resolvedJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_draft", Arguments: `{"action":"resolve","draft_id":"` + held.Draft.ID + `","resolution":"silent"}`})
	if err != nil || !strings.Contains(resolvedJSON, `"state":"dropped"`) {
		t.Fatalf("chat_draft silent = %s, err = %v", resolvedJSON, err)
	}

	taskJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_task", Arguments: `{"action":"create","room_id":"` + room.ID + `","title":"Review","owner_id":"` + credential.Agent.ID + `"}`})
	if err != nil {
		t.Fatalf("chat_task create error = %v", err)
	}
	var taskResult struct {
		Task channels.Message `json:"task"`
	}
	if err := json.Unmarshal([]byte(taskJSON), &taskResult); err != nil || taskResult.Task.TaskState != string(channels.TaskStateOpen) {
		t.Fatalf("chat_task create = %s, err %v", taskJSON, err)
	}
	updatedTaskJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_task", Arguments: `{"action":"update","task_id":"` + taskResult.Task.ID + `","state":"done"}`})
	if err != nil || !strings.Contains(updatedTaskJSON, `"task_state":"done"`) {
		t.Fatalf("chat_task update = %s, err %v", updatedTaskJSON, err)
	}
	listedTaskJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_task", Arguments: `{"action":"list","room_id":"` + room.ID + `"}`})
	if err != nil || !strings.Contains(listedTaskJSON, taskResult.Task.ID) {
		t.Fatalf("chat_task list = %s, err %v", listedTaskJSON, err)
	}

	reminderJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_remind", Arguments: `{"action":"set","after":"1m","note":"resume"}`})
	if err != nil {
		t.Fatalf("chat_remind set error = %v", err)
	}
	var reminderResult struct {
		Reminder channels.Reminder `json:"reminder"`
	}
	if err := json.Unmarshal([]byte(reminderJSON), &reminderResult); err != nil || reminderResult.Reminder.State != channels.ReminderPending {
		t.Fatalf("chat_remind set = %s, err %v", reminderJSON, err)
	}
	listedReminderJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_remind", Arguments: `{"action":"list","state":"pending"}`})
	if err != nil || !strings.Contains(listedReminderJSON, reminderResult.Reminder.ID) {
		t.Fatalf("chat_remind list = %s, err %v", listedReminderJSON, err)
	}
	cancelledReminderJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_remind", Arguments: `{"action":"cancel","reminder_id":"` + reminderResult.Reminder.ID + `"}`})
	if err != nil || !strings.Contains(cancelledReminderJSON, `"state":"cancelled"`) {
		t.Fatalf("chat_remind cancel = %s, err %v", cancelledReminderJSON, err)
	}
}

func TestNamedAgentToolsVerifyAssignedIndependentRun(t *testing.T) {
	ctx := context.Background()
	service, err := channels.Open(filepath.Join(t.TempDir(), "channels"), nil)
	if err != nil {
		t.Fatalf("channels.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	owner, err := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Andy"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	room, err := service.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Build", CreatedBy: "local-user",
		Members: []channels.RoomMember{
			{MemberType: channels.MemberHuman, MemberID: "local-user"},
			{MemberType: channels.MemberAgent, MemberID: owner.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	client, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(owner) error = %v", err)
	}
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("tools.New() error = %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)
	kit.SetChatAgent(client)
	assertDefinitionPresent(t, kit.Definitions(), "chat_verify")
	assertDefinitionPresent(t, kit.Definitions(), "chat_roster")
	assertDefinitionPresent(t, kit.Definitions(), "chat_send")
	for _, projectTool := range []string{"read_file", "apply_patch", "bash", "web_search", "load_skill", "tool_search"} {
		assertDefinitionPresent(t, kit.Definitions(), projectTool)
	}
	if !kit.SupportsTool("set_session_workspace") {
		t.Fatal("named-agent surface must retain deferred session workspace support")
	}
	rosterJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_roster", Arguments: `{"action":"create","room_id":"` + room.ID + `","name":"Reviewer"}`})
	if err != nil || !strings.Contains(rosterJSON, `"name":"Reviewer"`) || !strings.Contains(rosterJSON, `"state":"pending"`) {
		t.Fatalf("chat_roster create = %s, err = %v", rosterJSON, err)
	}

	reviewer, err := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Independent Reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	invitedJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_roster", Arguments: `{"action":"invite","room_id":"` + room.ID + `","agent_id":"` + reviewer.Agent.ID + `"}`})
	if err != nil || !strings.Contains(invitedJSON, reviewer.Agent.ID) {
		t.Fatalf("chat_roster invite = %s, err = %v", invitedJSON, err)
	}
	reviewerClient, err := service.BindAgent(ctx, reviewer.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}

	taskJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_task", Arguments: `{"action":"create","room_id":"` + room.ID + `","title":"Fix callback","owner_id":"` + owner.Agent.ID + `","verification_required":true}`})
	if err != nil {
		t.Fatalf("chat_task create error = %v", err)
	}
	var taskResult struct {
		Task channels.Message `json:"task"`
	}
	if err := json.Unmarshal([]byte(taskJSON), &taskResult); err != nil {
		t.Fatalf("decode task result: %v", err)
	}
	revisedJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_task", Arguments: `{"action":"revise","room_id":"` + room.ID + `","task_id":"` + taskResult.Task.ID + `","body":"Reject replayed and expired state"}`})
	if err != nil || !strings.Contains(revisedJSON, `"task_goal_revision":2`) {
		t.Fatalf("chat_task revise = %s, err = %v", revisedJSON, err)
	}
	ownerClient, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatalf("bind task owner: %v", err)
	}
	producerRun, err := ownerClient.StartWorkRun(ctx, channels.WorkRunStartParams{WorkID: taskResult.Task.ID, Kind: channels.WorkRunProducer, RequestID: "tool-test-producer"})
	if err != nil {
		t.Fatalf("start producer run: %v", err)
	}
	candidate, err := ownerClient.AddWorkArtifact(ctx, channels.WorkArtifactAddParams{WorkID: taskResult.Task.ID, RunID: producerRun.ID, Kind: channels.WorkArtifactCandidate, URI: "artifact://tool-test"})
	if err != nil {
		t.Fatalf("add candidate: %v", err)
	}
	promoted, err := ownerClient.PromoteWorkCandidate(ctx, channels.WorkCandidatePromoteParams{WorkID: taskResult.Task.ID, RunID: producerRun.ID, ArtifactRef: candidate.ID, RequestID: "tool-test-promotion", SelectionReason: "single candidate"})
	if err != nil {
		t.Fatalf("promote candidate: %v", err)
	}
	if _, err := ownerClient.FinishWorkRun(ctx, channels.WorkRunFinishParams{WorkID: taskResult.Task.ID, RunID: producerRun.ID, State: channels.WorkRunCompleted}); err != nil {
		t.Fatalf("finish producer run: %v", err)
	}
	verifierRun, err := client.StartWorkRun(ctx, channels.WorkRunStartParams{
		WorkID: taskResult.Task.ID, NamedAgentID: reviewer.Agent.ID, Kind: channels.WorkRunVerifier, SessionRef: "independent-verifier",
	})
	if err != nil {
		t.Fatalf("start verifier run: %v", err)
	}
	kit.SetChatAgent(reviewerClient)
	reportArgs := fmt.Sprintf(`{"action":"add_artifact","work_id":%q,"run_id":%q,"artifact_kind":"report","uri":"artifact://review-report"}`, taskResult.Task.ID, verifierRun.ID)
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: reportArgs}); err != nil {
		t.Fatalf("record verifier report: %v", err)
	}
	finishArgs := fmt.Sprintf(`{"action":"finish_run","work_id":%q,"run_id":%q,"run_state":"completed","outcome":"block"}`, taskResult.Task.ID, verifierRun.ID)
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_work", Arguments: finishArgs}); err != nil {
		t.Fatalf("finish verifier run: %v", err)
	}
	verifyArgs := fmt.Sprintf(
		`{"room_id":%q,"task_id":%q,"goal_revision":%d,"candidate_revision":%d,"decision":"block","report":"Replay still succeeds.","run_ref":%q}`,
		room.ID, taskResult.Task.ID, promoted.GoalRevision, promoted.CandidateRevision, verifierRun.ID,
	)
	kit.SetChatAgent(ownerClient)
	producerVerification := strings.Replace(verifyArgs, verifierRun.ID, producerRun.ID, 1)
	if _, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_verify", Arguments: producerVerification}); err == nil {
		t.Fatal("chat_verify accepted the owner's producer run as independent verification")
	}
	kit.SetChatAgent(reviewerClient)
	verifiedJSON, err := kit.Execute(ctx, providers.ToolCall{Name: "chat_verify", Arguments: verifyArgs})
	if err != nil || !strings.Contains(verifiedJSON, `"decision":"block"`) || !strings.Contains(verifiedJSON, `"kind":"verification_feedback"`) {
		t.Fatalf("chat_verify = %s, err = %v", verifiedJSON, err)
	}
}

func assertDefinitionPresent(t *testing.T, definitions []providers.ToolDefinition, name string) {
	t.Helper()
	for _, definition := range definitions {
		if definition.Name == name {
			return
		}
	}
	t.Fatalf("tool definition %q missing", name)
}

func assertDefinitionMissing(t *testing.T, definitions []providers.ToolDefinition, name string) {
	t.Helper()
	for _, definition := range definitions {
		if definition.Name == name {
			t.Fatalf("tool definition %q unexpectedly present", name)
		}
	}
}

func assertDefinitionProperties(t *testing.T, definitions []providers.ToolDefinition, name string, properties ...string) {
	t.Helper()
	for _, definition := range definitions {
		if definition.Name != name {
			continue
		}
		schemaProperties, _ := definition.InputSchema["properties"].(map[string]any)
		for _, property := range properties {
			if _, ok := schemaProperties[property]; !ok {
				t.Fatalf("tool definition %q missing property %q", name, property)
			}
		}
		return
	}
	t.Fatalf("tool definition %q missing", name)
}
