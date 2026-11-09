package main

import "strings"

func backgroundScenario(prompt, commandID string) bool {
	if !strings.HasPrefix(prompt, "background_") {
		return false
	}
	scenario := strings.TrimPrefix(prompt, "background_")
	result := func(text, origin string, failed bool) {
		frame := map[string]any{"type": "result", "subtype": "success", "is_error": failed, "result": text, "usage": map[string]int{"input_tokens": 90, "output_tokens": 30, "cache_creation_input_tokens": 10, "cache_read_input_tokens": 60}}
		if origin != "" {
			frame["origin"] = map[string]string{"kind": origin}
		}
		if failed {
			frame["subtype"] = "error_during_execution"
		}
		send(frame)
	}
	if scenario == "zero_turn_user_result" {
		send(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "", "num_turns": 0})
		return true
	}
	if scenario == "discarded_input" {
		send(map[string]any{"type": "command_lifecycle", "command_uuid": commandID, "state": "queued"})
		send(map[string]any{"type": "command_lifecycle", "command_uuid": commandID, "state": "discarded"})
		return true
	}
	if scenario == "queued_input" {
		if commandID == "" {
			panic("user input must carry a command UUID")
		}
		send(map[string]any{"type": "command_lifecycle", "command_uuid": commandID, "state": "queued"})
		send(map[string]any{"type": "system", "subtype": "task_started", "task_id": "old-agent", "tool_use_id": "old-spawn", "task_type": "local_agent"})
		result("Stale background result.", "", false)
		send(map[string]any{"type": "command_lifecycle", "command_uuid": commandID, "state": "started"})
		result("Final summary.", "", false)
		return true
	}
	start := func(id, tool string) {
		send(map[string]any{"type": "system", "subtype": "task_started", "task_id": id, "tool_use_id": tool, "task_type": "local_agent", "description": "Scout"})
	}
	terminal := func(id, tool string) {
		send(map[string]any{"type": "system", "subtype": "task_notification", "task_id": id, "tool_use_id": tool, "status": "completed"})
	}
	if scenario == "shell" {
		send(map[string]any{"type": "system", "subtype": "task_started", "task_id": "server", "tool_use_id": "bash", "task_type": "local_bash"})
		result("Launched.", "", false)
		return true
	}
	send(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "spawn-1", "name": "Agent", "input": map[string]any{"description": "Scout", "run_in_background": !strings.HasPrefix(scenario, "foreground")}}}}})
	if scenario != "receipt_only" {
		if scenario == "foreground_without_tool_id" {
			start("agent-1", "")
		} else {
			start("agent-1", "spawn-1")
		}
	}
	if scenario == "terminal_first" {
		terminal("agent-1", "spawn-1")
	}
	status := "async_launched"
	if strings.HasPrefix(scenario, "foreground") {
		status = "completed"
	}
	send(map[string]any{"type": "user", "tool_use_result": map[string]any{"agentId": "agent-1", "status": status, "isAsync": !strings.HasPrefix(scenario, "foreground")}, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "spawn-1", "content": "Agent accepted"}}}})
	if scenario == "parallel" || strings.HasPrefix(scenario, "coalesced_receipts") {
		start("agent-2", "spawn-2")
	}
	if scenario == "already_consumed" {
		terminal("agent-1", "spawn-1")
		send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start"}})
		result("Launched.", "", false)
		return true
	}
	result("Launched.", "", false)
	if strings.HasPrefix(scenario, "foreground") {
		return true
	}
	if scenario == "crash" {
		return false
	}
	if scenario == "cancel" {
		send(map[string]any{"type": "system", "subtype": "task_progress", "task_id": "agent-1", "description": "Background waiting"})
		return true
	}
	send(map[string]any{"type": "stream_event", "parent_tool_use_id": "spawn-1", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "CHILD private output"}}})
	terminal("agent-1", "spawn-1")
	if scenario == "terminal_first" {
		start("agent-1", "spawn-1")
	}
	if scenario == "parallel" {
		result("First summary.", "task-notification", false)
		terminal("agent-2", "spawn-2")
	}
	if strings.HasPrefix(scenario, "coalesced_receipts") {
		terminal("agent-2", "spawn-2")
		receipt := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "", "num_turns": 0}
		if scenario == "coalesced_receipts" {
			receipt["origin"] = map[string]string{"kind": "task-notification"}
		}
		send(receipt)
	}
	if scenario == "zero_turn_wake_error" {
		send(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": "", "num_turns": 0})
		return true
	}
	if scenario == "streamed_wake" {
		send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start"}})
		send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Partial summary"}}})
		send(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_delta", "usage": map[string]int{"input_tokens": 3, "output_tokens": 7}}})
	}
	result("Final summary.", "task-notification", scenario == "wake_error")
	return true
}
