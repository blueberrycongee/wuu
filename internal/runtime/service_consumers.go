package runtime

import (
	"context"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const (
	toolExecutionConsumer    = "wuu.tool-execution"
	sessionExecutionConsumer = "wuu.session-execution"
)

// Native entry points participate as consumers independently of the services
// they use. Keeping these owners separate avoids a false dependency cycle when
// a driver itself consumes model or checkpoint services.
type runtimeServiceConsumer struct {
	id           string
	requirements []pluginhost.ServiceRequirement
	mu           sync.Mutex
	state        pluginhost.State
}

func runtimeServiceConsumers(driverProfile string) []pluginhost.Client {
	tool := &runtimeServiceConsumer{
		id: toolExecutionConsumer, state: pluginhost.StatePrepared,
		requirements: []pluginhost.ServiceRequirement{
			{Name: pluginhost.SecurityAuthorizeService, MajorVersion: pluginhost.SecurityServiceMajor},
			{Name: pluginhost.ProcessSandboxService, MajorVersion: pluginhost.SecurityServiceMajor},
		},
	}
	loop := &runtimeServiceConsumer{id: sessionExecutionConsumer, state: pluginhost.StatePrepared}
	if profile := strings.TrimSpace(driverProfile); profile != "" {
		loop.requirements = []pluginhost.ServiceRequirement{{Name: DriverServicePrefix + profile, MajorVersion: 1, Required: true}}
	}
	return []pluginhost.Client{tool, loop}
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
func (c *runtimeServiceConsumer) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = pluginhost.StateStopped
	return nil
}
