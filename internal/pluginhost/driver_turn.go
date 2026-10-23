package pluginhost

import "github.com/blueberrycongee/wuu/internal/pluginprotocol"

func DriverTurnDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: pluginprotocol.DriverTurnService, Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "read", InputSchema: "driver.turn.scope.v1", OutputSchema: "host.model.request.v1"},
			{Name: "model", InputSchema: "driver.turn.model.input.v1", OutputSchema: "driver.turn.model.output.v1"},
			{Name: "tools", InputSchema: "driver.turn.tools.input.v1", OutputSchema: "driver.turn.tools.output.v1"},
			{Name: "complete", InputSchema: "driver.turn.complete.input.v1", OutputSchema: "driver.turn.complete.output.v1"},
		},
	}
}
