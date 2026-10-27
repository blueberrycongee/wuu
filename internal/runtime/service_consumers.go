package runtime

import (
	"context"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/titleservice"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

const (
	toolExecutionConsumer    = "wuu.tool-execution"
	sessionExecutionConsumer = "wuu.session-execution"
	modelExecutionConsumer   = "wuu.model-execution"
)

// Native entry points participate as consumers independently of the services
// they use. Keeping these owners separate avoids a false dependency cycle when
// a driver itself consumes model or checkpoint services.
type runtimeServiceConsumer struct {
	id           string
	requirements []pluginhost.ServiceRequirement
	mu           sync.Mutex
	state        pluginhost.State
	cleanup      func(context.Context) error
}

func runtimeServiceConsumers(driverProfile string) []pluginhost.Client {
	tool := &runtimeServiceConsumer{
		id: toolExecutionConsumer, state: pluginhost.StatePrepared,
		requirements: []pluginhost.ServiceRequirement{
			{Name: toolservice.ServiceName, MajorVersion: 1, Required: true},
			{Name: toolbatch.ServiceName, MajorVersion: 1},
			{Name: pluginhost.SecurityAuthorizeService, MajorVersion: pluginhost.SecurityServiceMajor, Required: true},
			{Name: pluginhost.ProcessSandboxService, MajorVersion: pluginhost.SecurityServiceMajor},
		},
	}
	profile := strings.TrimSpace(driverProfile)
	if profile == "" {
		profile = "default"
	}
	loop := &runtimeServiceConsumer{id: sessionExecutionConsumer, state: pluginhost.StatePrepared,
		requirements: []pluginhost.ServiceRequirement{{Name: pluginprotocol.AgentPolicyService, MajorVersion: 1, Required: true}, {Name: titleservice.ServiceName, MajorVersion: 1}, {Name: compactionservice.ServiceName, MajorVersion: 1, Required: true}, {Name: promptservice.ServiceName, MajorVersion: 1, Required: true}, {Name: DriverServicePrefix + profile, MajorVersion: loopdriver.DriverServiceMajor, Required: true}},
	}
	state := &runtimeServiceConsumer{id: StateServicesConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{
		{Name: credentialstore.ServiceName, MajorVersion: 1},
		{Name: pluginsettings.ServiceName, MajorVersion: 1},
		{Name: config.ServiceName, MajorVersion: 1, Required: true},
		{Name: ArtifactServiceName, MajorVersion: 1},
		{Name: pluginhost.KernelDataQueryService, MajorVersion: pluginhost.DataQueryServiceMajor},
	}}
	return []pluginhost.Client{tool, loop, state, &runtimeServiceConsumer{id: modelExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: selectedModelService, MajorVersion: 1, Required: true}, {Name: capacityPolicyService, MajorVersion: 1, Required: true}, {Name: capacityResourceService, MajorVersion: 1, Required: true}}}, &runtimeServiceConsumer{id: engineExecutionConsumer, state: pluginhost.StatePrepared}}
}

func (c *runtimeServiceConsumer) ID() string { return c.id }
func (c *runtimeServiceConsumer) Status() pluginhost.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return pluginhost.Status{ID: c.id, State: c.state}
}
func (c *runtimeServiceConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (c *runtimeServiceConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return append([]pluginhost.ServiceRequirement(nil), c.requirements...)
}
func (c *runtimeServiceConsumer) Activate(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = pluginhost.StateActive
	return nil
}
func (c *runtimeServiceConsumer) Close(ctx context.Context) error {
	var err error
	if c.cleanup != nil {
		err = c.cleanup(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = pluginhost.StateStopped
	return err
}

// Selection is independent of activation success. Failed selected consumers
// must still attempt their normal admission and report the missing dependency.
func selectsRuntimeModule(host *pluginhost.Host, id string) bool {
	for _, status := range host.Statuses() {
		if status.ID == id {
			return true
		}
	}
	return false
}
