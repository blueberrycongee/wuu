package pluginhost

import "context"

type processSessionDeliveryKey struct{}

// WithProcessSessionDelivery grants delivery only to a host-owned process's
// captured recipient. Plugins cannot construct this internal context value.
func WithProcessSessionDelivery(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, processSessionDeliveryKey{}, sessionID)
}

func HasProcessSessionDelivery(ctx context.Context, sessionID string) bool {
	id, ok := ctx.Value(processSessionDeliveryKey{}).(string)
	return ok && id != "" && id == sessionID
}

// KernelProcessDescriptor exposes generation-owned streaming shell processes.
// Monitors and command-driven automation share execution and cleanup semantics.
func KernelProcessDescriptor() ServiceDescriptor {
	return ServiceDescriptor{
		Name: "host.process", Version: "1.0.0",
		Methods: []ServiceMethodDescriptor{
			{Name: "start", InputSchema: "host.process.start.input.v1", OutputSchema: "host.process.snapshot.v1"},
			{Name: "read", InputSchema: "host.process.read.input.v1", OutputSchema: "host.process.output.v1"},
			{Name: "stop", InputSchema: "host.process.stop.input.v1", OutputSchema: "host.process.snapshot.v1"},
			{Name: "send", InputSchema: "host.process.send.input.v1", OutputSchema: "host.session.send.result.v1"},
		},
	}
}
