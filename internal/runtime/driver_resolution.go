package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// DriverServicePrefix is the service-namespace convention a loop driver
// plugin contributes under: a driver profile resolves to the versioned
// service "driver.<profile>" major 1.
const DriverServicePrefix = "driver."

// registryDriverInvoker adapts the service registry to loopdriver.RemoteInvoker.
type registryDriverInvoker struct {
	host    *pluginhost.Host
	service string
}

func (i registryDriverInvoker) InvokeDriver(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	result, hostErr := i.host.CallService(ctx, sessionExecutionConsumer, i.service, method, params)
	if hostErr != nil {
		return nil, fmt.Errorf("%s: %s", hostErr.Code, hostErr.Message)
	}
	return result, nil
}

// generationGatewayRegistrar retains the same callback table as the selected
// provider. Publishing a new generation must not redirect an old execution.
type generationGatewayRegistrar struct {
	table         *driverGatewayTable
	ownerPluginID string
}

func (r generationGatewayRegistrar) RegisterGateway(executionID string, gateway loopdriver.KernelGateway) (func(), error) {
	return r.table.register(executionID, gateway, r.ownerPluginID)
}

// currentDriverGatewayTable captures the table when a driver binding is made.
func (s *Session) currentDriverGatewayTable() *driverGatewayTable {
	if s == nil {
		return nil
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	if s.pluginGeneration == nil {
		return nil
	}
	return s.pluginGeneration.driverGateways
}

// resolveLoopDriver binds one session's selected driver profile to a Driver:
// an empty profile selects driver.default. Every live provider becomes a
// RemoteDriver routed through the registry; anything else
// fails closed so history stays readable but new runs are rejected with a
// diagnostic error.
func resolveLoopDriver(profile string, host *pluginhost.Host, table *driverGatewayTable) loopdriver.Driver {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = "default"
	}
	service := DriverServicePrefix + profile
	var registry *pluginhost.ServiceRegistry
	if host != nil {
		registry = host.ServiceRegistry()
	}
	if registry == nil {
		return loopdriver.FailClosedDriver{Profile: profile, Reason: "service registry is unavailable"}
	}
	ownerPluginID, found := registry.ProviderPluginID(service, 1)
	if !found {
		return loopdriver.FailClosedDriver{Profile: profile, Reason: fmt.Sprintf("no provider for service %s major version 1", service)}
	}
	if table == nil {
		return loopdriver.FailClosedDriver{Profile: profile, Reason: "driver gateway table is unavailable"}
	}
	return &loopdriver.RemoteDriver{
		Profile:   profile,
		ServiceID: service,
		Invoker:   registryDriverInvoker{host: host, service: service},
		Gateways:  generationGatewayRegistrar{table: table, ownerPluginID: ownerPluginID},
	}
}
