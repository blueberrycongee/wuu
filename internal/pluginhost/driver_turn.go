package pluginhost

import "github.com/blueberrycongee/wuu/internal/pluginprotocol"

func DriverTurnDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: pluginprotocol.DriverTurnService, Version: "4.2.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "resolve_wait", InputSchema: "driver.turn.resolve-wait.input.v1", OutputSchema: "host.durable-wait.resolve.output.v1"},
			{Name: "register_wait", InputSchema: "driver.turn.register-wait.input.v1", OutputSchema: "host.durable-wait.register.output.v1"},
			{Name: "read", InputSchema: "driver.turn.scope.v1", OutputSchema: "host.model.request.v1"},
			{Name: "evaluate_policy", InputSchema: "driver.turn.policy.input.v1", OutputSchema: "agent.policy.decision.v1"},
			{Name: "settings", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.settings.v1"},
			{Name: "context_state", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.context-state.v1"},
			{Name: "save_context_state", InputSchema: "driver.turn.context-state.input.v1", OutputSchema: "driver.turn.context-state.output.v1"},
			{Name: "observe", InputSchema: "driver.turn.observe.input.v1", OutputSchema: "driver.turn.observe.output.v1"},
			{Name: "compact", InputSchema: "driver.turn.compact.input.v1", OutputSchema: "driver.turn.compact.output.v1"},
			{Name: "start_compaction", InputSchema: "driver.turn.compaction.input.v1", OutputSchema: "driver.turn.compaction.output.v1"},
			{Name: "read_compaction", InputSchema: "driver.turn.compaction.input.v1", OutputSchema: "driver.turn.compaction.output.v1"},
			{Name: "apply_compaction", InputSchema: "driver.turn.compaction.input.v1", OutputSchema: "driver.turn.compaction.output.v1"},
			{Name: "cancel_compaction", InputSchema: "driver.turn.compaction.input.v1", OutputSchema: "driver.turn.compaction.output.v1"},
			{Name: "admit_input", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.admission.output.v1"},
			{Name: "request_context", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.request-context.output.v1"},
			{Name: "transform_request", InputSchema: "driver.turn.model.input.v1", OutputSchema: "host.model.request.v1"},
			{Name: "context", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.context.output.v1"},
			{Name: "archive_context", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.archive.output.v1"},
			{Name: "replace_context", InputSchema: "driver.turn.context.input.v1", OutputSchema: "driver.turn.context.output.v1"},
			{Name: "model", InputSchema: "driver.turn.model.input.v1", OutputSchema: "driver.turn.model.output.v2"},
			{Name: "model_receipts", InputSchema: "driver.turn.model-receipts.input.v1", OutputSchema: "driver.turn.model-receipts.output.v1"},
			{Name: "model_with_tools", InputSchema: "driver.turn.model-tools.input.v1", OutputSchema: "driver.turn.model.output.v2"},
			{Name: "tools", InputSchema: "driver.turn.tools.input.v1", OutputSchema: "driver.turn.tools.output.v1"},
			{Name: "recover_tools", InputSchema: "driver.turn.recover-tools.input.v1", OutputSchema: "driver.turn.tools.output.v1"},
			{Name: "complete", InputSchema: "driver.turn.complete.input.v1", OutputSchema: "driver.turn.complete.output.v1"},
		},
	}
}
