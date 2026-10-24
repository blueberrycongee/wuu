package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// driverGatewayTable routes turn resources and checkpoints by execution ID and
// provider identity. Entries exist only while the owning driver is running.
type driverGatewayTable struct {
	mu       sync.Mutex
	gateways map[string]driverGatewayEntry
}

type driverGatewayEntry struct {
	gateway       loopdriver.KernelGateway
	ownerPluginID string
}

func newDriverGatewayTable() *driverGatewayTable {
	return &driverGatewayTable{gateways: make(map[string]driverGatewayEntry)}
}

func (t *driverGatewayTable) register(executionID string, gateway loopdriver.KernelGateway, ownerPluginID string) (func(), error) {
	executionID = strings.TrimSpace(executionID)
	if executionID == "" {
		return nil, fmt.Errorf("driver gateway registration requires an execution id")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.gateways[executionID]; exists {
		return nil, fmt.Errorf("driver gateway for execution %s is already registered", executionID)
	}
	t.gateways[executionID] = driverGatewayEntry{gateway: gateway, ownerPluginID: ownerPluginID}
	return sync.OnceFunc(func() {
		t.mu.Lock()
		delete(t.gateways, executionID)
		t.mu.Unlock()
	}), nil
}

func (t *driverGatewayTable) lookup(executionID string) (driverGatewayEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.gateways[strings.TrimSpace(executionID)]
	return entry, ok
}

// driverGatewayCaller validates one gateway service call and returns the
// gateway it routes to. Authorization is per execution: only the plugin that
// owns the execution's driver run may drive its gateway.
func (t *driverGatewayTable) authorizedGateway(callerPluginID string, executionID string) (loopdriver.KernelGateway, *pluginhost.HostServiceError) {
	entry, ok := t.lookup(executionID)
	if !ok {
		return nil, &pluginhost.HostServiceError{Code: "execution_not_found", Message: fmt.Sprintf("no active driver run for execution %s", strings.TrimSpace(executionID))}
	}
	if entry.ownerPluginID != "" && entry.ownerPluginID != callerPluginID {
		return nil, &pluginhost.HostServiceError{Code: "execution_not_authorized", Message: fmt.Sprintf("plugin %q does not own the driver run for execution %s", callerPluginID, strings.TrimSpace(executionID))}
	}
	return entry.gateway, nil
}

type driverCheckpointInvoker struct {
	parent *kernelHostServices
}

func (k *driverCheckpointInvoker) ID() string                { return k.parent.ID() }
func (k *driverCheckpointInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *driverCheckpointInvoker) Close(context.Context) error {
	return nil
}

func (k *driverCheckpointInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var decoded loopdriver.DriverCheckpointParams
	if err := json.Unmarshal(params.Params, &decoded); err != nil {
		return nil, serviceError("invalid_request", "driver.checkpoint params: "+err.Error())
	}
	gateway, hostErr := k.parent.driverGateways.authorizedGateway(params.Caller, decoded.ExecutionID)
	if hostErr != nil {
		return nil, hostErr
	}
	if err := gateway.WriteCheckpoint(ctx, decoded.Checkpoint); err != nil {
		return nil, err
	}
	return json.RawMessage(`{}`), nil
}
