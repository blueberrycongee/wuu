package runtime

import (
	"context"
	"encoding/json"
	"errors"
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

// driverResolver retains the selected generation through execution cleanup.
// Root workers acquire the current generation at each execution boundary;
// conversations and their workers inherit the thread's retained generation.
func (s *Session) driverResolver(parent *PluginGeneration) loopdriver.AcquireFunc {
	return func(ctx context.Context, expected *loopdriver.Binding) (driver loopdriver.Driver, binding loopdriver.Binding, release func(), err error) {
		generation := parent
		if generation == nil {
			generation = s.retainPluginGeneration()
		} else {
			generation.retain()
		}
		release = func() { s.releasePluginGeneration(generation) }
		defer func() {
			if err != nil {
				release()
			}
		}()
		profile := strings.TrimSpace(s.DriverProfile)
		if profile == "" {
			profile = "default"
		}
		if expected != nil {
			if expected.Profile == "" || expected.ProviderID == "" {
				return nil, binding, release, fmt.Errorf("legacy loop driver %s@%s lacks a provider binding; restore and explicitly migrate its original composition", expected.DriverID, expected.DriverVersion)
			}
			profile = expected.Profile
		}
		providerID := ""
		if generation != nil && generation.driverGateways != nil {
			driver = resolveLoopDriver(profile, generation.host, generation.driverGateways)
			if generation.host != nil && generation.host.ServiceRegistry() != nil {
				providerID, _ = generation.host.ServiceRegistry().ProviderPluginID(DriverServicePrefix+profile, 1)
			}
		} else if s.StreamRunner != nil {
			// Embedded callers own their explicit driver and its lifetime.
			driver = s.StreamRunner.LoopDriver
		}
		if driver == nil {
			return nil, binding, release, errors.New("loop driver is not configured")
		}
		descriptor, err := driver.Descriptor(ctx)
		if err != nil {
			return nil, binding, release, fmt.Errorf("resolve loop driver: %w", err)
		}
		if generation == nil || generation.driverGateways == nil {
			providerID = descriptor.ID
		}
		binding = loopdriver.Binding{Profile: profile, ProviderID: providerID, ContractVersion: loopdriver.ContractVersion, DriverID: descriptor.ID, DriverVersion: descriptor.Version}
		if expected != nil && *expected != binding {
			return nil, binding, release, fmt.Errorf("loop driver binding requires %+v; selected binding is %+v", *expected, binding)
		}
		return driver, binding, release, nil
	}
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
