package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type steeringBoundaryProvider struct{ *nativeEngineProvider }

func (*steeringBoundaryProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*steeringBoundaryProvider) Close(context.Context) error                       { return nil }

type steeringBoundarySession struct {
	agentengine.Session
	result error
}

func (s *steeringBoundarySession) Steer(context.Context, providers.ChatMessage) (string, error) {
	return "accepted-turn", s.result
}

func TestSelectedSteeringPreservesOnlyConfirmedNonDelivery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session agentengine.Session
		want    error
	}{
		{"unsupported-method", agentengine.FailedSession(errors.New("unused")), agentengine.ErrSteerUnsupported},
		{"unsupported-protocol", &steeringBoundarySession{result: agentengine.ErrSteerUnsupported}, agentengine.ErrSteerUnsupported},
		{"rejected", &steeringBoundarySession{result: agentengine.ErrSteerRejected}, agentengine.ErrSteerRejected},
		{"accepted", &steeringBoundarySession{}, nil},
		{"uncertain", &steeringBoundarySession{result: errors.New("connection lost after send")}, errors.New("unknown")},
		{"uncertain-text", &steeringBoundarySession{result: errors.New(agentengine.ErrSteerUnsupported.Error())}, errors.New("unknown")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &steeringBoundaryProvider{&nativeEngineProvider{state: pluginhost.StatePrepared, sessions: map[string]*nativeEngineSession{"bound": {session: tc.session, owner: engineExecutionConsumer, engine: "wuu"}}}}
			consumer := &runtimeServiceConsumer{id: engineExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: "engine.wuu", MajorVersion: 1, Required: true}}}
			registry, conflicts := pluginhost.BuildServiceRegistry(p, consumer)
			if len(conflicts) != 0 {
				t.Fatal(conflicts)
			}
			host := pluginhost.New(p, consumer)
			host.AttachServiceRegistry(registry, conflicts)
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			selected := &selectedEngineSession{generation: &PluginGeneration{host: host}, id: "wuu", handle: pluginprotocol.EngineSessionParams{SessionID: "bound"}}
			ref, err := selected.Steer(context.Background(), providers.ChatMessage{Role: "user", Content: "follow up"})
			switch tc.name {
			case "accepted":
				if err != nil || ref != "accepted-turn" {
					t.Fatalf("accepted=%q %v", ref, err)
				}
			case "uncertain", "uncertain-text":
				if err == nil || errors.Is(err, agentengine.ErrSteerUnsupported) || errors.Is(err, agentengine.ErrSteerRejected) {
					t.Fatalf("unknown became retryable: %v", err)
				}
			default:
				if !errors.Is(err, tc.want) {
					t.Fatalf("lost confirmed non-delivery across public registry: %v", err)
				}
			}
		})
	}
}
