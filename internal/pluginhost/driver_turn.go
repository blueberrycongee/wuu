package pluginhost

import "github.com/blueberrycongee/wuu/internal/pluginprotocol"

func DriverTurnDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: pluginprotocol.DriverTurnService, Version: "3.1.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "read", InputSchema: "driver.turn.scope.v1", OutputSchema: "host.model.request.v1"},
			{Name: "admit_input", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.admission.output.v1"},
			{Name: "request_context", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.request_context.output.v1"},
			{Name: "transform_request", InputSchema: "driver.turn.model.input.v1", OutputSchema: "host.model.request.v1"},
			{Name: "context", InputSchema: "driver.turn.scope.v1", OutputSchema: "driver.turn.context.output.v1"},
			{Name: "replace_context", InputSchema: "driver.turn.context.input.v1", OutputSchema: "driver.turn.context.output.v1"},
			{Name: "model", InputSchema: "driver.turn.model.input.v1", OutputSchema: "driver.turn.model.output.v2"},
			{Name: "model_with_tools", InputSchema: "driver.turn.model_tools.input.v1", OutputSchema: "driver.turn.model.output.v2"},
			{Name: "tools", InputSchema: "driver.turn.tools.input.v1", OutputSchema: "driver.turn.tools.output.v1"},
			{Name: "complete", InputSchema: "driver.turn.complete.input.v1", OutputSchema: "driver.turn.complete.output.v1"},
		},
	}
}
