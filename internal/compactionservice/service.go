package compactionservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type Service struct {
	mu    sync.Mutex
	state pluginhost.State
}

func NewService() *Service    { return &Service{state: pluginhost.StatePrepared} }
func (s *Service) ID() string { return NativeModule }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := make([]pluginhost.ServiceMethodDescriptor, 0, 8)
	for _, name := range []string{"describe", "compact", "window", "handoff", "note-plan", "compact-note", "recover", "note-attempt"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: ServiceName + "." + name + ".input.v1", OutputSchema: ServiceName + "." + name + ".output.v1"})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: "host.model.invoke", MajorVersion: 1, Required: true}}
}
func (s *Service) Prepare(ctx context.Context) error { return ctx.Err() }
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return fmt.Errorf("compaction provider cannot activate from %s", s.state)
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func decode(raw json.RawMessage, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("request object required")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one request object")
	}
	return nil
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Status().State != pluginhost.StateActive {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "compaction provider is not active"}
	}
	if p.Service != ServiceName {
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown compaction service"}
	}
	var result any
	var err error
	switch p.Method {
	case "describe":
		var in DescribeRequest
		if err = decode(p.Params, &in); err == nil {
			result, err = nativeDescribe(ctx, in)
		}
	case "compact":
		var in Request
		if err = decode(p.Params, &in); err == nil {
			client, _ := ctx.Value(modelClientKey{}).(providers.Client)
			result, err = nativeCompact(ctx, in, client)
		}
	case "window":
		var in WindowRequest
		if err = decode(p.Params, &in); err == nil {
			_, selected, policyErr := legacyPolicy(ctx, in.PolicyKey)
			if policyErr != nil {
				return nil, policyErr
			}
			if selected != nil && selected.Descriptor.Version < 3 {
				return nil, &pluginhost.HostServiceError{Code: "window_unsupported", Message: "selected policy does not provide context windows"}
			}
			result, err = BuildWindow(in)
		}
	case "handoff":
		var in HandoffRequest
		if err = decode(p.Params, &in); err == nil {
			result, err = nativeHandoff(ctx, in)
		}
	case "note-plan", "compact-note":
		var in NoteRequest
		if err = decode(p.Params, &in); err == nil {
			if p.Method == "note-plan" {
				result, err = prepareNativeNote(ctx, in)
				break
			}
			operation := "compact_with_note"
			var out pluginhost.CompactionOutput
			out, err = nativeNote(ctx, in, operation)
			if err == nil {
				if out.CoveredMessages <= 0 || out.CoveredMessages > len(out.Messages) {
					return nil, fmt.Errorf("invalid note coverage")
				}
				if err = providers.ValidateToolCallHistory(out.Messages); err == nil {
					result = NoteReplacement{Messages: out.Messages, CoveredMessages: out.CoveredMessages}
				}
			}
		}
	case "note-attempt":
		var in NoteAttemptRequest
		if err = decode(p.Params, &in); err == nil {
			result, err = nativeNoteAttempt(in)
		}
	case "recover":
		var in RecoveryRequest
		if err = decode(p.Params, &in); err == nil {
			result, err = nativeRecover(ctx, in)
		}
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown compaction method"}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
