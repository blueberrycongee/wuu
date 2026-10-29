package appserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const pluginTurnOriginNamespace = "wuu.plugin_turn_origins.v1"

type pluginTurnOrigin struct {
	HandoffDigest string `json:"handoff_digest,omitempty"`
	Composition   string `json:"composition"`
	Session       string `json:"session"`
}

func readPluginTurnOrigin(ctx context.Context, binding *runtime.State, pluginID, requestID string) (pluginTurnOrigin, persistence.Revision, bool, error) {
	view, err := binding.Documents.Read(ctx, persistence.Query{Namespace: pluginTurnOriginNamespace, Keys: []string{pluginSessionRequestClientID(pluginID, requestID)}})
	if err != nil {
		return pluginTurnOrigin{}, 0, false, err
	}
	var origin pluginTurnOrigin
	if len(view.Documents) == 0 {
		return origin, view.Revision, false, nil
	}
	err = json.Unmarshal(view.Documents[0].Value, &origin)
	return origin, view.Revision, true, err
}

// Record before admission: queued work and outbox replay must never infer its
// producer from a session's later binding. Request IDs remain plugin-wide in
// the shared state authority; reusing one from another scope fails explicitly.
func recordPluginTurnOrigin(ctx context.Context, binding *runtime.State, pluginID, requestID string, origin pluginTurnOrigin) error {
	raw, err := json.Marshal(origin)
	if err != nil {
		return err
	}
	for {
		previous, revision, found, err := readPluginTurnOrigin(ctx, binding, pluginID, requestID)
		if err != nil {
			return err
		}
		if found {
			if previous != origin {
				return fmt.Errorf("plugin request %q conflicts with its original composition, session or handoff", requestID)
			}
			return nil
		}
		_, err = binding.Documents.Commit(ctx, persistence.Transaction{ID: rand.Text(), ExpectedRevision: revision,
			Writes: []persistence.Write{{Namespace: pluginTurnOriginNamespace, Key: pluginSessionRequestClientID(pluginID, requestID), Value: raw}}})
		if errors.Is(err, persistence.ErrConflict) {
			continue
		}
		return err
	}
}

func (s *Server) retainPluginTurnOrigin(binding *runtime.State, pluginID, requestID string) (*runtime.State, error) {
	origin, _, found, err := readPluginTurnOrigin(context.Background(), binding, pluginID, requestID)
	if err != nil {
		return nil, err
	}
	if !found || origin.Composition == binding.ExecutionCompositionID() {
		return binding.RetainExecution()
	}
	if origin.Composition == "" {
		return nil, errors.New("plugin lifecycle origin is the product generation, not this composition")
	}
	return s.rt.RetainCompositionExecution(origin.Composition)
}

func (s *Server) retainThreadExecutionState(binding *runtime.State, thread *runtime.ThreadRuntime) (*runtime.State, error) {
	if thread != nil && thread.CompositionID != "" {
		return s.rt.RetainCompositionExecution(thread.CompositionID)
	}
	return binding.RetainExecution()
}

func (s *Server) notifyPluginReferenceLifecycle(binding *runtime.State, reference *pluginTurnReference, input pluginhost.AgentTurnLifecycleInput) {
	if reference.Origin != nil {
		if held := reference.Origin.Retain(); held != nil {
			defer held.Close()
			binding = held
		}
		if pluginTurnLifecycleTerminal(input.State) {
			defer reference.Origin.Close()
		}
	}
	s.notifyPluginTurnLifecycleAsync(binding, reference.PluginID, input)
}
