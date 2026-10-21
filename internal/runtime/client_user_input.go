package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ClientUserInput is an exact message submitted by an authenticated application
// client. It is never inferred from plugin-generated text or tool arguments.
type ClientUserInput struct {
	Relation    *providers.MessageRelation `json:"relation,omitempty"`
	RequestID   string                     `json:"request_id"`
	Prompt      string                     `json:"prompt"`
	Attachments []ClientInputAttachment    `json:"attachments,omitempty"`
}
type clientUserInputKey struct{}
type clientUserInputProof struct {
	mu         sync.Mutex
	pluginID   string
	input      ClientUserInput
	target     string
	admission  context.Context
	dispatchID string
}

// WithClientUserInput is called only after client admission binds a trusted
// plugin identity. Host execution contexts propagate it across live callbacks;
// independently admitted background calls have no proof. A detached callback
// cannot extend the originating admission lifetime or mint another send.
func WithClientUserInput(ctx context.Context, pluginID string, input ClientUserInput) context.Context {
	input.Relation = providers.CloneMessageRelation(input.Relation)
	input.Attachments = append([]ClientInputAttachment(nil), input.Attachments...)
	return context.WithValue(ctx, clientUserInputKey{}, &clientUserInputProof{pluginID: pluginID, input: input, admission: ctx})
}
func AuthorizeClientUserInput(ctx context.Context, pluginID, requestID, target, prompt string, dispatchIDs ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, _ := ctx.Value(clientUserInputKey{}).(*clientUserInputProof)
	if proof == nil {
		return errors.New("user input requires a live authenticated client submission")
	}
	if err := proof.admission.Err(); err != nil {
		return errors.New("client user input admission has ended")
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(target) == "" || proof.pluginID != pluginID || proof.input.RequestID != requestID || proof.input.Prompt != prompt {
		return errors.New("user input does not match the admitted client submission")
	}
	if proof.target != "" && proof.target != target {
		return errors.New("user input is already bound to another session")
	}
	dispatchID := requestID
	if len(dispatchIDs) > 0 {
		dispatchID = dispatchIDs[0]
	}
	if strings.TrimSpace(dispatchID) == "" || (proof.dispatchID != "" && proof.dispatchID != dispatchID) {
		return errors.New("user input is already bound to another send request")
	}
	proof.dispatchID = dispatchID
	proof.target = target
	return nil
}

// AuthorizeClientUserInputRelation binds routing separately from the literal prompt.
func AuthorizeClientUserInputRelation(ctx context.Context, pluginID, requestID string, relation *providers.MessageRelation) error {
	proof, _ := ctx.Value(clientUserInputKey{}).(*clientUserInputProof)
	if proof == nil || ctx.Err() != nil || proof.admission.Err() != nil {
		return errors.New("relation requires a live authenticated client submission")
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	expected, err := json.Marshal(proof.input.Relation)
	if err != nil {
		return err
	}
	actual, err := json.Marshal(relation)
	if err != nil {
		return err
	}
	if proof.pluginID != pluginID || proof.input.RequestID != requestID || !bytes.Equal(expected, actual) {
		return errors.New("relation does not match the admitted client submission")
	}
	return nil
}
