package personalim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Only an exact command in a verified owner's received message enters this
// control path. Quoted messages, model output and source text never call it.
func channelCommand(ctx context.Context, client Client, text, agentID string) (bool, string, error) {
	if !strings.HasPrefix(text, "!wuu ") {
		return false, "", nil
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return true, "", errors.New("a command is required")
	}
	if (fields[1] == "help" || fields[1] == "tasks" || fields[1] == "pause" || fields[1] == "resume" || fields[1] == "approvals") && len(fields) != 2 {
		return true, "", errors.New("control commands must match the exact documented syntax")
	}
	switch fields[1] {
	case "help":
		return true, "!wuu tasks — task status\n!wuu stop TASK_ID REVISION — stop that task\n!wuu pause / !wuu resume — pause or resume Personal\n!wuu approvals — inspect pending actions\n!wuu approve ACTION_ID DIGEST REVISION — approve the exact action once\n!wuu reject ACTION_ID DIGEST REVISION — reject the exact action\nOther messages continue your Personal conversation in this thread.", nil
	case "tasks":
		var result struct {
			Tasks []struct {
				ID       string `json:"task_id"`
				Title    string `json:"title"`
				State    string `json:"state"`
				Revision int    `json:"revision"`
				Progress string `json:"progress"`
			} `json:"tasks"`
		}
		if err := invoke(ctx, client, "tasks.list", map[string]any{"agent_id": agentID}, &result); err != nil {
			return true, "", err
		}
		rows := []string{}
		for _, t := range result.Tasks {
			rows = append(rows, fmt.Sprintf("%s — %s\n%s revision %d\n%s", t.Title, t.State, t.ID, t.Revision, t.Progress))
		}
		if len(rows) == 0 {
			return true, "No tracked tasks.", nil
		}
		return true, strings.Join(rows, "\n\n"), nil
	case "stop":
		if len(fields) != 4 {
			return true, "", errors.New("use !wuu stop TASK_ID REVISION")
		}
		revision, err := strconv.Atoi(fields[3])
		if err != nil {
			return true, "", err
		}
		var result struct {
			Task struct {
				State    string `json:"state"`
				Revision int    `json:"revision"`
			} `json:"task"`
		}
		if err = invoke(ctx, client, "tasks.get", map[string]any{"task_id": fields[2]}, &result); err != nil {
			return true, "", err
		}
		if result.Task.State == "stopped" {
			return true, "Task is stopped.", nil
		}
		if result.Task.Revision != revision {
			return true, "", errors.New("task changed; inspect !wuu tasks before stopping")
		}
		if err = invoke(ctx, client, "tasks.stop", map[string]any{"task_id": fields[2], "expected_revision": revision}, nil); err != nil {
			return true, "", err
		}
		return true, "Task stop requested.", nil
	case "pause", "resume":
		var result struct {
			Agent struct {
				Status   string `json:"status"`
				Revision int    `json:"revision"`
			} `json:"agent"`
		}
		if err := invoke(ctx, client, "agents.open", map[string]any{"agent_id": agentID}, &result); err != nil {
			return true, "", err
		}
		target := "paused"
		if fields[1] == "resume" {
			target = "active"
		}
		if result.Agent.Status != target {
			if err := invoke(ctx, client, "agents."+fields[1], map[string]any{"agent_id": agentID, "expected_revision": result.Agent.Revision}, nil); err != nil {
				return true, "", err
			}
			return true, "Personal " + fields[1] + " requested.", nil
		}
		return true, "Personal is " + target + ".", nil
	case "approvals", "approve", "reject":
		var result struct {
			Kind    string `json:"kind"`
			Records []struct {
				Revision int    `json:"revision"`
				Status   string `json:"status"`
				Action   struct {
					ID            string          `json:"id"`
					Digest        string          `json:"digest"`
					Arguments     json.RawMessage `json:"arguments"`
					ArgumentsJSON string          `json:"arguments_json"`
					Scope         struct {
						SpaceID string `json:"space_id"`
					} `json:"scope"`
					Summary struct{ Title, Account, Target, Consequence string } `json:"summary"`
				} `json:"action"`
			} `json:"requests"`
		}
		if err := invokeReview(ctx, client, "review.list", map[string]any{}, &result); err != nil {
			return true, "", err
		}
		if result.Kind != "ready" {
			return true, "", errors.New("action review is unavailable")
		}
		if fields[1] == "approvals" {
			rows := []string{}
			for _, r := range result.Records {
				if r.Status == "awaiting_confirmation" {
					a := r.Action
					rows = append(rows, fmt.Sprintf("%s\nAccount: %s\nTarget: %s\nEffect: %s\nParameters: %s\n!wuu approve %s %s %d\n!wuu reject %s %s %d", a.Summary.Title, a.Summary.Account, a.Summary.Target, a.Summary.Consequence, a.ArgumentsJSON, a.ID, a.Digest, r.Revision, a.ID, a.Digest, r.Revision))
				}
			}
			if len(rows) == 0 {
				return true, "No pending action approvals.", nil
			}
			return true, strings.Join(rows, "\n\n"), nil
		}
		if len(fields) != 5 {
			return true, "", errors.New("use !wuu approve|reject ACTION_ID DIGEST REVISION")
		}
		revision, err := strconv.Atoi(fields[4])
		if err != nil {
			return true, "", err
		}
		for _, r := range result.Records {
			if r.Action.ID != fields[2] {
				continue
			}
			if r.Action.Digest != fields[3] {
				return true, "", errors.New("action digest changed; inspect !wuu approvals again")
			}
			if r.Status == "succeeded" || r.Status == "executing" || r.Status == "denied" || r.Status == "approved" {
				return true, "Action state: " + r.Status + ". It was not repeated.", nil
			}
			if r.Revision != revision {
				return true, "", errors.New("action revision changed; inspect !wuu approvals again")
			}
			choice := "approve_once"
			if fields[1] == "reject" {
				choice = "reject"
			}
			if err = invokeReview(ctx, client, "review.resolve", map[string]any{"space_id": r.Action.Scope.SpaceID, "id": r.Action.ID, "digest": r.Action.Digest, "expected_revision": revision, "choice": choice}, nil); err != nil {
				return true, "", err
			}
			return true, "Action decision recorded: " + choice + ".", nil
		}
		return true, "", errors.New("action not found; inspect !wuu approvals")
	default:
		return true, "", errors.New("unknown command; use !wuu help")
	}
}

func invokeReview(ctx context.Context, client Client, method string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	raw, err = client.Call(ctx, method, raw)
	if err != nil {
		return err
	}
	var envelope struct {
		OK    bool            `json:"ok"`
		Value json.RawMessage `json:"value"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if !envelope.OK || len(envelope.Value) == 0 {
		return fmt.Errorf("action review failed: %s", envelope.Error.Code)
	}
	if out != nil {
		return json.Unmarshal(envelope.Value, out)
	}
	return nil
}
