package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/approvefor"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type PersonalPreparedCommand struct {
	EffectID string              `json:"effect_id"`
	Command  tools.CommandAction `json:"command"`
}
type personalPreparedAction struct {
	EffectID  string
	Arguments json.RawMessage
}

func (p *personalActions) prepareActionEffect(ctx context.Context, kit *tools.Toolkit, call providers.ToolCall) (personalPreparedAction, error) {
	if call.Name == "write_file" || call.Name == "edit_file" {
		prepared, err := p.prepareFileEffect(ctx, kit, call)
		if err != nil {
			return personalPreparedAction{}, err
		}
		raw, err := json.Marshal(prepared.File)
		return personalPreparedAction{EffectID: prepared.EffectID, Arguments: raw}, err
	}
	var prepared PersonalPreparedCommand
	if err := p.callEffect(ctx, kit, "prepare-command", call, &prepared); err != nil {
		return personalPreparedAction{}, err
	}
	raw, err := json.Marshal(prepared.Command)
	return personalPreparedAction{EffectID: prepared.EffectID, Arguments: raw}, err
}

func (p *personalActions) describeCommand(raw json.RawMessage) (personalreview.Summary, error) {
	var a tools.CommandAction
	if err := decodePersonalAction(raw, &a); err != nil {
		return personalreview.Summary{}, err
	}
	if (a.Operation != "bash" && a.Operation != "process_start") || a.EnvironmentDigest == "" || !filepath.IsAbs(a.CWD) || len(a.Arguments) > 32*1024 {
		return personalreview.Summary{}, errors.New("unsupported Personal command action")
	}
	var instruction struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(a.Arguments, &instruction); err != nil {
		return personalreview.Summary{}, errors.New("invalid Personal command action")
	}
	control := a.Operation == "bash" && strings.TrimSpace(instruction.Action) != "" && strings.TrimSpace(instruction.Action) != "run"
	title := "Run a command"
	consequence := "Runs the displayed command in this folder. It may change files or contact services. This approval covers only this invocation."
	if control {
		title = "Control a running task"
		consequence = "Sends the displayed input or control instruction to this running task. Starting it earlier does not authorize this instruction."
	}
	if a.Operation == "process_start" {
		title = "Start a background task"
		consequence = "Starts the displayed command in this folder and keeps it running in the background. Later input requires a separate approval."
	}
	return personalreview.Summary{Title: title, Account: "Personal local commands", Target: a.CWD, Consequence: consequence}, nil
}

func (p *personalActions) commandRuleScope(raw json.RawMessage) (personalreview.RuleScope, error) {
	var a tools.CommandAction
	if err := decodePersonalAction(raw, &a); err != nil {
		return personalreview.RuleScope{}, err
	}
	if _, err := p.describeCommand(raw); err != nil {
		return personalreview.RuleScope{}, err
	}
	// Exact full invocation matching includes shell composition. Neither argv
	// prefixes nor strings from a purpose description grant future authority.
	a.CallID = ""
	a.SessionID = ""
	a.ActorID = ""
	key, err := json.Marshal(a)
	if err != nil {
		return personalreview.RuleScope{}, err
	}
	var instruction struct {
		Command   string `json:"command"`
		Action    string `json:"action"`
		Input     string `json:"input"`
		ProcessID string `json:"process_id"`
	}
	if err := json.Unmarshal(a.Arguments, &instruction); err != nil {
		return personalreview.RuleScope{}, err
	}
	description := "Run only this exact command in " + a.CWD + ": " + a.ResolvedCommand
	if a.Operation == "bash" && strings.TrimSpace(instruction.Action) != "" && strings.TrimSpace(instruction.Action) != "run" {
		description = "Apply only this exact instruction to process " + instruction.ProcessID + ": " + string(a.Arguments)
	}
	return personalreview.RuleScope{Key: personalHash(key), Description: description + " (same execution environment and permissions)"}, nil
}

func (p *personalActions) reviewCommand(ctx context.Context, input personalreview.ReviewInput) (approvefor.Decision, error) {
	var a tools.CommandAction
	if err := decodePersonalAction(input.Action.Arguments, &a); err != nil {
		return approvefor.Decision{Outcome: "deny", Reason: "Invalid frozen command action"}, nil
	}
	if a.Operation != input.Action.Operation {
		return approvefor.Decision{Outcome: "deny", Reason: "Command operation mismatch"}, nil
	}
	kit, err := p.toolkit(ctx, input.Action)
	if err != nil {
		return approvefor.Decision{Outcome: "unsure", Reason: "Command runtime unavailable; nothing executed"}, nil
	}
	if err = kit.ValidateCommandAction(ctx, a); err != nil {
		return approvefor.Decision{Outcome: "deny", Reason: "Command or execution environment changed; nothing executed: " + err.Error()}, nil
	}
	p.kitsMu.Lock()
	token := p.effects[input.Action.ID]
	p.kitsMu.Unlock()
	if token == "" {
		prepared, err := p.prepareActionEffect(ctx, kit, providers.ToolCall{ID: a.CallID, Name: a.Operation, Arguments: string(a.Arguments)})
		if err != nil {
			return approvefor.Decision{Outcome: "unsure", Reason: "Host command effect unavailable; nothing executed"}, nil
		}
		var current tools.CommandAction
		if err = json.Unmarshal(prepared.Arguments, &current); err != nil {
			return approvefor.Decision{}, err
		}
		before, _ := json.Marshal(a)
		after, _ := json.Marshal(current)
		if !bytes.Equal(before, after) {
			_ = p.callEffect(ctx, nil, "release", PersonalEffectReference{EffectID: prepared.EffectID}, nil)
			return approvefor.Decision{Outcome: "deny", Reason: "Frozen command changed; nothing executed"}, nil
		}
		p.kitsMu.Lock()
		if p.effects[input.Action.ID] == "" {
			p.effects[input.Action.ID] = prepared.EffectID
			prepared.EffectID = ""
		}
		p.kitsMu.Unlock()
		if prepared.EffectID != "" {
			_ = p.callEffect(ctx, nil, "release", PersonalEffectReference{EffectID: prepared.EffectID}, nil)
		}
	}
	return p.reviewDecision(ctx, input)
}

func (p *personalActions) executeCommand(ctx context.Context, action personalreview.Action) (json.RawMessage, error) {
	var a tools.CommandAction
	if err := decodePersonalAction(action.Arguments, &a); err != nil {
		return nil, err
	}
	if a.Operation != action.Operation {
		return nil, errors.New("command operation mismatch")
	}
	result, err := p.executeActionEffect(ctx, action.ID)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New("command execution returned an error; inspect the result before retrying")
	}
	return json.Marshal(map[string]any{"receipt": map[string]any{"operation": a.Operation, "target": a.CWD, "status": "succeeded", "action_id": action.ID}, "tool_result": result})
}

func (m *personalEffectMechanisms) prepareCommand(ctx context.Context, scope *personalEffectScope, held *runtime.State, owner string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.kit == nil {
		return nil, errors.New("command effect requires captured toolkit")
	}
	var call providers.ToolCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, err
	}
	action, err := scope.kit.PrepareCommandAction(ctx, call)
	if err != nil {
		return nil, err
	}
	// The command's working directory stays in the Personal files domain. Its
	// ordinary filesystem sandbox separately enforces every process access.
	if err = personalEffectSafeFile(scope.server, tools.FileAction{Operation: action.Operation, Arguments: action.Arguments, CWD: action.CWD, Target: action.CWD}); err != nil {
		return nil, err
	}
	kit, err := scope.kit.CloneForRoot(scope.kit.RootDir())
	if err != nil {
		return nil, err
	}
	state := held.Retain()
	if state == nil {
		return nil, errors.New("personal effect state released")
	}
	resource := newPersonalEffectResource()
	resource.owner, resource.registry, resource.server, resource.state = owner, m.registry, scope.server, state
	resource.use = func(ctx context.Context) (toolresult.Result, error) {
		ctx = runtime.WithInvocationState(ctx, state)
		metadata, found, err := state.Sessions.Find(kit.SessionID())
		if err != nil {
			return toolresult.Result{}, err
		}
		if !found || metadata.ArchivedAt != nil || metadata.UserSpaceID != scope.server.rt.UserBinding().UserSpaceID {
			return toolresult.Result{}, errors.New("Personal command session is unavailable")
		}
		if err = scope.server.AuthorizeResource(ctx, "personal/command.execute", access.Resource{Kind: "user-space", ID: metadata.UserSpaceID}, access.Resource{Kind: "session", ID: kit.SessionID()}); err != nil {
			return toolresult.Result{}, err
		}
		return kit.ExecuteCommandAction(ctx, action)
	}
	id, err := m.bridge.add(resource)
	if err != nil {
		resource.finish(context.Background())
		return nil, err
	}
	return json.Marshal(PersonalPreparedCommand{EffectID: id, Command: action})
}

func (s *Server) personalCommandGate(ctx context.Context, kit *tools.Toolkit, call providers.ToolCall) (toolresult.Result, error) {
	sessionID := ""
	if kit != nil {
		sessionID = kit.SessionID()
	}
	raw, err := s.invokePersonalCoordination(ctx, "command-gate", personalCoordinationRequest{SessionID: sessionID, Call: call}, kit)
	if err != nil {
		return toolresult.Result{}, err
	}
	var result toolresult.Result
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (p *personalActions) registerCommandOperations(operations map[string]personalreview.Operation) {
	for _, name := range []string{"bash", "process_start"} {
		operations[name] = personalreview.Operation{Version: "personal-local-command.v1", Effect: personalreview.ExternalWrite, Describe: p.describeCommand, Execute: p.executeCommand, RuleScope: p.commandRuleScope}
	}
}
