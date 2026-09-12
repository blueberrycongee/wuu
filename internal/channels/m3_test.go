package channels

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestM3TaskCreateWakeUpdateDone(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha, beta)

	task, err := service.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Count to ten",
		Body:    "keep going",
		OwnerID: beta.Agent.ID,
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if task.Kind != MessageTask || task.TaskTitle != "Count to ten" || task.TaskOwner != beta.Agent.ID || task.TaskState != string(TaskStateOpen) {
		t.Fatalf("created task = %#v", task)
	}
	if got := sink.take(); len(got) != 1 || got[0] != beta.Agent.ID {
		t.Fatalf("task create wake = %v, want beta", got)
	}

	betaClient, err := service.BindAgent(ctx, beta.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(beta) error = %v", err)
	}
	betaSession := bindTestWorkSession(t, service, betaClient, room.ID, task.ID, "m3-beta-work")
	betaCheck, err := betaSession.Check(ctx)
	if err != nil || len(betaCheck.Items) != 1 || betaCheck.Items[0].Kind != InboxTask || betaCheck.Items[0].MessageID != task.ID {
		t.Fatalf("beta check = %#v, err = %v", betaCheck, err)
	}

	updated, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID:  task.ID,
		State:   TaskStateDoing,
		AgentID: beta.Agent.ID,
		Token:   beta.Token,
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if updated.TaskState != string(TaskStateDoing) {
		t.Fatalf("updated task state = %q", updated.TaskState)
	}
	if got := sink.take(); len(got) != 1 || got[0] != beta.Agent.ID {
		t.Fatalf("task update wake = %v, want beta", got)
	}
	betaCheck, err = betaSession.Check(ctx)
	if err != nil || len(betaCheck.Items) != 1 || betaCheck.Items[0].MessageID != task.ID {
		t.Fatalf("beta second check = %#v, err = %v", betaCheck, err)
	}
	progress, err := service.SendAgent(ctx, AgentSendParams{
		RoomID: room.ID, AgentID: beta.Agent.ID, Token: beta.Token,
		Body: "halfway complete", ReplyTo: task.ID, BasisSeq: task.Seq,
	})
	if err != nil || progress.Status != SendCommitted || progress.Message.ThreadID != task.ID {
		t.Fatalf("task progress = %#v, err = %v", progress, err)
	}
	if got := sink.take(); len(got) != 1 || got[0] != alpha.Agent.ID {
		t.Fatalf("task reply should wake its visible author: %v", got)
	}
	threadMessages, err := service.ListMessages(ctx, room.ID, task.Seq, 10)
	if err != nil || len(threadMessages) != 1 || threadMessages[0].Body != "halfway complete" {
		t.Fatalf("task thread messages = %#v, err = %v", threadMessages, err)
	}

	done, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID:  task.ID,
		State:   TaskStateDone,
		AgentID: beta.Agent.ID,
		Token:   beta.Token,
	})
	if err != nil || done.TaskState != string(TaskStateDone) {
		t.Fatalf("done UpdateTask() = %#v, err = %v", done, err)
	}
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("completed work must not restart its owner: %v", got)
	}

	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID:  task.ID,
		State:   "bad",
		AgentID: beta.Agent.ID,
		Token:   beta.Token,
	}); err == nil {
		t.Fatal("invalid task state accepted")
	}
}

func TestM3TaskHumanCreatesAndReassignsOwner(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind:      RoomChannel,
		Name:      "team",
		CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: alpha.Agent.ID},
			{MemberType: MemberAgent, MemberID: beta.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}

	task, err := service.CreateTaskHuman(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Investigate",
		Body:    "check logs",
		OwnerID: alpha.Agent.ID,
		HumanID: "local-user",
	})
	if err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	if got := sink.take(); len(got) != 1 || got[0] != alpha.Agent.ID {
		t.Fatalf("human task create wake = %v, want alpha", got)
	}

	if _, err := service.CreateTaskHuman(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Bad",
		OwnerID: "unknown-agent",
		HumanID: "local-user",
	}); err == nil {
		t.Fatal("unknown owner accepted")
	}

	reassigned, err := service.UpdateTaskHuman(ctx, TaskUpdateParams{
		TaskID:  task.ID,
		OwnerID: beta.Agent.ID,
		HumanID: "local-user",
	})
	if err != nil || reassigned.TaskOwner != beta.Agent.ID {
		t.Fatalf("reassign = %#v, err = %v", reassigned, err)
	}
	wakes := sink.take()
	if len(wakes) != 1 || wakes[0] != beta.Agent.ID {
		t.Fatalf("reassign wake = %v, want beta", wakes)
	}
	alphaCheck, err := service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil || len(alphaCheck.Items) != 0 || len(alphaCheck.Collaboration) != 0 {
		t.Fatalf("alpha agent-level check consumed reassigned Work = %#v, err = %v", alphaCheck, err)
	}
	betaCheck, err := service.Check(ctx, beta.Agent.ID, beta.Token)
	if err != nil || len(betaCheck.Items) != 0 || len(betaCheck.Collaboration) != 0 {
		t.Fatalf("beta agent-level check consumed reassigned Work = %#v, err = %v", betaCheck, err)
	}
}

func TestM3TaskAuth(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	gamma := createTestAgent(t, service, "Gamma")
	room := createTestRoom(t, service, alpha, beta)

	task, err := service.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Mine",
		OwnerID: alpha.Agent.ID,
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if _, err := service.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Outsider",
		OwnerID: alpha.Agent.ID,
		AgentID: gamma.Agent.ID,
		Token:   gamma.Token,
	}); err == nil {
		t.Fatal("outsider agent created task")
	}
	if _, err := service.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "Bad token",
		OwnerID: alpha.Agent.ID,
		AgentID: alpha.Agent.ID,
		Token:   "bad",
	}); err == nil {
		t.Fatal("bad token accepted")
	}
	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID:  task.ID,
		State:   TaskStateDoing,
		AgentID: beta.Agent.ID,
		Token:   beta.Token,
	}); err == nil {
		t.Fatal("non-owner updated task")
	}
	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID:  "msg-does-not-exist",
		State:   TaskStateDoing,
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task update error = %v", err)
	}
}

func TestM3ReminderSelfWakeExactlyOnceAndCancel(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	fixedNow := now
	service := openTestService(t, sink)
	service.now = func() time.Time { return fixedNow }
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, alpha)

	reminder, err := service.SetReminder(ctx, ReminderSetParams{
		AgentID:  alpha.Agent.ID,
		Token:    alpha.Token,
		FireAt:   now.Add(2 * time.Minute),
		Note:     "ping",
		RoomID:   room.ID,
		ThreadID: "",
	})
	if err != nil {
		t.Fatalf("SetReminder() error = %v", err)
	}
	if reminder.State != ReminderPending {
		t.Fatalf("reminder state = %q", reminder.State)
	}

	if _, err := service.SetReminder(ctx, ReminderSetParams{
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
		FireAt:  now.Add(30 * time.Second),
		Note:    "too soon",
	}); err == nil {
		t.Fatal("reminder too soon accepted")
	}

	wake, err := service.FireDueReminders(ctx)
	if err != nil || len(wake) != 0 {
		t.Fatalf("FireDueReminders early = %v, err = %v", wake, err)
	}

	fixedNow = now.Add(2 * time.Minute)
	wake, err = service.FireDueReminders(ctx)
	if err != nil || len(wake) != 1 || wake[0] != alpha.Agent.ID {
		t.Fatalf("FireDueReminders fire = %v, err = %v", wake, err)
	}
	if got := sink.take(); len(got) != 1 || got[0] != alpha.Agent.ID {
		t.Fatalf("reminder wake delivered = %v, want alpha", got)
	}
	wake, err = service.FireDueReminders(ctx)
	if err != nil || len(wake) != 0 {
		t.Fatalf("FireDueReminders repeat = %v, err = %v", wake, err)
	}

	check, err := service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil || len(check.Reminders) != 1 || check.Reminders[0].ID != reminder.ID {
		t.Fatalf("check reminders = %#v, err = %v", check.Reminders, err)
	}
	check, err = service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil || len(check.Reminders) != 0 {
		t.Fatalf("check after pull reminders = %#v, err = %v", check.Reminders, err)
	}

	pendingReminder, err := service.SetReminder(ctx, ReminderSetParams{
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
		FireAt:  now.Add(5 * time.Minute),
		Note:    "later",
	})
	if err != nil {
		t.Fatalf("SetReminder pending error = %v", err)
	}
	cancelled, err := service.CancelReminder(ctx, ReminderCancelParams{
		AgentID:    alpha.Agent.ID,
		Token:      alpha.Token,
		ReminderID: pendingReminder.ID,
	})
	if err != nil || cancelled.State != ReminderCancelled {
		t.Fatalf("cancel = %#v, err = %v", cancelled, err)
	}
	fixedNow = now.Add(6 * time.Minute)
	wake, err = service.FireDueReminders(ctx)
	if err != nil || len(wake) != 0 {
		t.Fatalf("FireDueReminders after cancel = %v, err = %v", wake, err)
	}

	state, err := service.WakeState(ctx, alpha.Agent.ID)
	if err != nil || state.Outstanding {
		t.Fatalf("wake state after check = %#v, err = %v", state, err)
	}
}

func TestM3ReminderRestartFriendlyDBState(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	fixedNow := now
	service := openTestService(t, sink)
	service.now = func() time.Time { return fixedNow }
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, alpha)

	reminder, err := service.SetReminder(ctx, ReminderSetParams{
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
		FireAt:  now.Add(2 * time.Minute),
		Note:    "restart",
		RoomID:  room.ID,
	})
	if err != nil {
		t.Fatalf("SetReminder() error = %v", err)
	}
	fixedNow = now.Add(2 * time.Minute)
	if _, err := service.FireDueReminders(ctx); err != nil {
		t.Fatalf("FireDueReminders() error = %v", err)
	}
	reminders, err := service.ListReminders(ctx, ReminderListParams{
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
		State:   ReminderFired,
	})
	if err != nil || len(reminders) != 1 || reminders[0].ID != reminder.ID {
		t.Fatalf("fired reminders = %#v, err = %v", reminders, err)
	}

	newService, err := Open(service.dir, sink)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer newService.Close()
	wake, err := newService.FireDueReminders(ctx)
	if err != nil || len(wake) != 0 {
		t.Fatalf("FireDueReminders after restart = %v, err = %v", wake, err)
	}
}

func TestM3ThreadLoopBudgetSixAgentOnlySuppressedAndHumanReset(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	humanRoom, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind:      RoomChannel,
		Name:      "team",
		CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: alpha.Agent.ID},
			{MemberType: MemberAgent, MemberID: beta.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}

	root, err := service.SendHuman(ctx, HumanSendParams{
		RoomID:  humanRoom.ID,
		HumanID: "local-user",
		Body:    "discuss",
	})
	if err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if got := sink.take(); len(got) != 2 {
		t.Fatalf("human message should wake both members: %v", got)
	}
	for _, id := range []string{alpha.Agent.ID, beta.Agent.ID} {
		if err := service.ClearWakeOnCheck(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	basis := root.Message.Seq
	for i := 1; i <= 5; i++ {
		agent := alpha
		if i%2 == 0 {
			agent = beta
		}
		body := fmt.Sprintf("agent-%d", i)
		if i == 5 {
			body = "@Beta agent-5"
		}
		result, err := service.SendAgent(ctx, AgentSendParams{
			RoomID:   humanRoom.ID,
			AgentID:  agent.Agent.ID,
			Token:    agent.Token,
			Body:     body,
			ReplyTo:  root.Message.ID,
			BasisSeq: basis,
		})
		if err != nil || result.Status != SendCommitted {
			t.Fatalf("send %d = %#v, err = %v", i, result, err)
		}
		basis = result.Message.Seq
		root.Message = result.Message
		wakes := sink.take()
		if i == 1 && len(wakes) != 0 || i > 1 && len(wakes) != 1 {
			t.Fatalf("reply %d wakes = %v", i, wakes)
		}
		for _, id := range wakes {
			if err := service.ClearWakeOnCheck(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}

	suppressedMention, err := service.SendAgent(ctx, AgentSendParams{
		RoomID:   humanRoom.ID,
		AgentID:  alpha.Agent.ID,
		Token:    alpha.Token,
		Body:     "@Beta are you there?",
		ReplyTo:  root.Message.ID,
		BasisSeq: basis,
	})
	if err != nil || suppressedMention.Status != SendCommitted {
		t.Fatalf("suppressed mention send = %#v, err = %v", suppressedMention, err)
	}
	if got := sink.take(); len(got) != 0 {
		t.Fatalf("6th shared-room message should be suppressed, got %v", got)
	}

	var collaborationCount int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collaboration_messages WHERE room_id = ?`, humanRoom.ID).Scan(&collaborationCount); err != nil {
		t.Fatal(err)
	}
	if collaborationCount != ThreadStreakCap {
		t.Fatalf("room agent collaboration messages = %d, want %d before suppression", collaborationCount, ThreadStreakCap)
	}

	reset, err := service.SendHuman(ctx, HumanSendParams{
		RoomID:  humanRoom.ID,
		HumanID: "local-user",
		Body:    "reset",
		ReplyTo: root.Message.ID,
	})
	if err != nil {
		t.Fatalf("human reset send error = %v", err)
	}
	if got := sink.take(); len(got) != 2 {
		t.Fatalf("human reset should wake both members, got %v", got)
	}
	for _, id := range []string{alpha.Agent.ID, beta.Agent.ID} {
		if err := service.ClearWakeOnCheck(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	afterReset, err := service.SendAgent(ctx, AgentSendParams{
		RoomID:   humanRoom.ID,
		AgentID:  alpha.Agent.ID,
		Token:    alpha.Token,
		Body:     "@Beta now it works",
		ReplyTo:  root.Message.ID,
		BasisSeq: reset.Message.Seq,
	})
	if err != nil || afterReset.Status != SendCommitted {
		t.Fatalf("after reset send = %#v, err = %v", afterReset, err)
	}
	if got := sink.take(); len(got) != 1 || got[0] != beta.Agent.ID {
		t.Fatalf("@mention should wake the visible member after human reset, got %v", got)
	}
}

func TestM3HumanMentionAndAck(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	room, err := service.CreateRoom(ctx, CreateRoomParams{
		Kind:      RoomChannel,
		Name:      "team",
		CreatedBy: "local-user",
		Members: []RoomMember{
			{MemberType: MemberHuman, MemberID: "local-user"},
			{MemberType: MemberAgent, MemberID: alpha.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}

	if _, err := service.SendAgent(ctx, AgentSendParams{
		RoomID:   room.ID,
		AgentID:  alpha.Agent.ID,
		Token:    alpha.Token,
		Body:     "hi @local-user check this",
		BasisSeq: 0,
	}); err != nil {
		t.Fatalf("SendAgent() error = %v", err)
	}

	status, err := service.HumanMentionStatus(ctx, "local-user")
	if err != nil || len(status) != 1 || status[0].UnreadCount != 1 || status[0].RoomID != room.ID {
		t.Fatalf("HumanMentionStatus = %#v, err = %v", status, err)
	}
	mentions, err := service.ListHumanMentions(ctx, "local-user", room.ID)
	if err != nil || len(mentions) != 1 || mentions[0].AuthorID != alpha.Agent.ID {
		t.Fatalf("ListHumanMentions = %#v, err = %v", mentions, err)
	}

	if err := service.AckHumanMentions(ctx, room.ID, "local-user"); err != nil {
		t.Fatalf("AckHumanMentions() error = %v", err)
	}
	status, err = service.HumanMentionStatus(ctx, "local-user")
	if err != nil || len(status) != 0 {
		t.Fatalf("status after ack = %#v, err = %v", status, err)
	}
	mentions, err = service.ListHumanMentions(ctx, "local-user", room.ID)
	if err != nil || len(mentions) != 0 {
		t.Fatalf("mentions after ack = %#v, err = %v", mentions, err)
	}

	alphaCheck, err := service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil || len(alphaCheck.Items) != 0 {
		t.Fatalf("agent check should not include human mentions: %#v", alphaCheck)
	}
}

func TestM3TaskListAndOwnerOnlyUpdate(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha, beta)

	if _, err := service.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "A",
		OwnerID: alpha.Agent.ID,
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
	}); err != nil {
		t.Fatalf("create A error = %v", err)
	}
	if _, err := service.CreateTask(ctx, TaskCreateParams{
		RoomID:  room.ID,
		Title:   "B",
		OwnerID: beta.Agent.ID,
		AgentID: alpha.Agent.ID,
		Token:   alpha.Token,
	}); err != nil {
		t.Fatalf("create B error = %v", err)
	}
	alphaTasks, err := service.ListTasks(ctx, TaskListParams{RoomID: room.ID, AgentID: alpha.Agent.ID, Token: alpha.Token})
	if err != nil || len(alphaTasks) != 2 {
		t.Fatalf("list all = %d, err = %v", len(alphaTasks), err)
	}
	betaTasks, err := service.ListTasks(ctx, TaskListParams{RoomID: room.ID, OwnerID: beta.Agent.ID, AgentID: beta.Agent.ID, Token: beta.Token})
	if err != nil || len(betaTasks) != 1 || betaTasks[0].TaskTitle != "B" {
		t.Fatalf("list beta = %#v, err = %v", betaTasks, err)
	}
}
