package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/personalim"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// PersonalChannelRequest is an attestation from the selected messaging adapter,
// not an identity claim accepted from ordinary Personal client JSON. The adapter
// verifies the platform event before supplying its literal native input.
type PersonalChannelRequest struct {
	Binding personalim.Binding `json:"binding"`
	Method  string             `json:"method"`
	Input   json.RawMessage    `json:"input"`
}

// HandlePersonalChannel receives actualCallerID from the authenticated host
// service gateway. Both native and process messaging providers use this path.
func (h *UserHost) HandlePersonalChannel(ctx context.Context, actualCallerID string, raw json.RawMessage) (json.RawMessage, error) {
	var request PersonalChannelRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	snapshot, release := h.runtime.RetainExtensionSnapshot()
	defer release()
	if snapshot.PluginHost == nil {
		return nil, errors.New("messaging host unavailable")
	}
	registry := snapshot.PluginHost.ServiceRegistry()
	if runtime.HasInvocationState(ctx) {
		state, err := runtime.RetainProductInvocationState(ctx)
		if err != nil {
			return nil, err
		}
		defer state.Close()
		if state.Host == nil || state.Host.ServiceRegistry() != registry {
			return nil, errors.New("channel belongs to a retired provider generation")
		}
	}
	selected, ok := registry.ProviderPluginIDFor(PersonalMessagingConsumer, PersonalMessagingService, 1)
	if !ok || selected != actualCallerID {
		return nil, errors.New("channel input requires the selected messaging provider")
	}
	authorization := executionauth.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		result, err := snapshot.PluginHost.CallService(ctx, executionauth.ConsumerModule, executionauth.ServiceName, method, params)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	record, err := authorization.ReadOwner(ctx, executionauth.OwnerEnrollmentRef{ID: request.Binding.EnrollmentID})
	if err != nil {
		return nil, err
	}
	expected := executionauth.OwnerEnrollment{ID: request.Binding.EnrollmentID, Principal: principal.Identity{Kind: "user", Issuer: "wuu:im:" + request.Binding.Platform, Tenant: request.Binding.TenantID, Subject: request.Binding.SenderID}, UserSpaceID: h.runtime.UserBinding().UserSpaceID, ProductDomain: h.runtime.UserBinding().Domain(), ConversationID: request.Binding.ConversationID}
	if record.Revoked || record != expected {
		return nil, errors.New("channel binding does not match active owner enrollment")
	}
	clean, err := validateChannelSource(request.Binding, request.Method, request.Input)
	if err != nil {
		return nil, err
	}
	request.Input = clean
	owner, err := principal.EnrolledOwner(record.Principal, record.ID, func() bool {
		h.mu.Lock()
		closed := h.closed
		h.mu.Unlock()
		current := h.runtime.ExtensionSnapshot()
		return !closed && current.PluginHost != nil && current.PluginHost.ServiceRegistry() == registry
	}, func(checkCtx context.Context) error {
		current, err := authorization.ReadOwner(checkCtx, executionauth.OwnerEnrollmentRef{ID: record.ID})
		if err != nil {
			return err
		}
		if current != record || current.Revoked {
			return errors.New("messaging owner enrollment was revoked")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	client, err := NewPersonalChannelClient(ctx, h, owner, registry)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.Call(ctx, request.Method, request.Input)
}

// HandleOwnerEnrollment is the embedding side of the same declared host gateway
// that process plugins call through host.execution.authorization.
func (h *UserHost) HandleOwnerEnrollment(ctx context.Context, actualCallerID string, raw json.RawMessage) (json.RawMessage, error) {
	return h.runtime.HandleExecutionAuthorization(ctx, actualCallerID, raw)
}

// The selected adapter attests platform provenance; the host binds that source
// to the approved owner, stable native message identity and exact requested effect.
func validateChannelSource(binding personalim.Binding, method string, raw json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	var source personalim.Incoming
	sourceRaw := fields["channel_source"]
	delete(fields, "channel_source")
	clean, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	switch method {
	case "agents.ensure_default", "agents.open", "agents.channel_history", "agents.history", "agents.messages.status", "agents.attachments.read", "tasks.get", "tasks.list", "review.list":
		return clean, nil
	case "agents.send", "agents.messages.resume", "agents.pause", "agents.resume", "tasks.stop", "review.resolve":
	default:
		return nil, errors.New("operation is unavailable to a messaging channel")
	}
	if json.Unmarshal(sourceRaw, &source) != nil || source.MessageID == "" || source.Platform != binding.Platform || source.TenantID != binding.TenantID || source.SenderID != binding.SenderID || source.ConversationID != binding.ConversationID {
		return nil, errors.New("channel source does not match enrolled owner")
	}
	if method == "agents.send" || method == "agents.messages.resume" {
		proof, err := personalChannelInput(method, clean)
		if err != nil {
			return nil, err
		}
		key := source.Platform + "\x00" + source.TenantID + "\x00" + source.ConversationID + "\x00" + source.MessageID
		if proof.RequestID != fmt.Sprintf("im-%x", sha256.Sum256([]byte(key))) {
			return nil, errors.New("channel request identity differs from native message")
		}
		if method == "agents.send" && proof.Prompt != source.Text {
			return nil, errors.New("channel prompt differs from native message")
		}
		return clean, nil
	}
	var input struct {
		TaskID   string `json:"task_id"`
		ID       string `json:"id"`
		Digest   string `json:"digest"`
		Revision int    `json:"expected_revision"`
		Choice   string `json:"choice"`
	}
	if err := json.Unmarshal(clean, &input); err != nil {
		return nil, err
	}
	command := ""
	switch method {
	case "agents.pause":
		command = "!wuu pause"
	case "agents.resume":
		command = "!wuu resume"
	case "tasks.stop":
		command = "!wuu stop " + input.TaskID + " " + strconv.Itoa(input.Revision)
	case "review.resolve":
		choice := ""
		if input.Choice == "approve_once" {
			choice = "approve"
		} else if input.Choice == "reject" {
			choice = "reject"
		}
		if choice == "" {
			return nil, errors.New("unsupported channel approval choice")
		}
		command = "!wuu " + choice + " " + input.ID + " " + input.Digest + " " + strconv.Itoa(input.Revision)
	}
	if strings.Join(strings.Fields(source.Text), " ") != command {
		return nil, errors.New("channel control does not match owner's exact command")
	}
	return clean, nil
}
