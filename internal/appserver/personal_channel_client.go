package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// PersonalChannelClient is an in-process application client for a trusted,
// owner-enrolled messaging transport. Platform adapters verify their native
// sender, tenant and conversation binding before constructing this client.
// It uses the ordinary plugin client RPC handler, not a plugin-owned authority
// claim, and resolves the selected Personal provider anew for every operation.
type PersonalChannelClient struct {
	host      *UserHost
	registry  *pluginhost.ServiceRegistry
	ctx       context.Context
	cancel    context.CancelFunc
	input     *io.PipeWriter
	output    *io.PipeReader
	done      chan struct{}
	mu        sync.Mutex
	next      uint64
	pendingMu sync.Mutex
	pending   map[string]chan personalChannelResponse
}

// NewPersonalChannelClient never enrolls an identity. The caller must come from
// the host transport's verified, persisted owner pairing, not decoded JSON.
func NewPersonalChannelClient(ctx context.Context, host *UserHost, owner principal.Verified, expected *pluginhost.ServiceRegistry) (*PersonalChannelClient, error) {
	if host == nil || expected == nil || host.runtime.UserBinding().Domain() != "personal" {
		return nil, errors.New("Personal channel requires a Personal user host")
	}
	lifetime, cancel := context.WithCancel(ctx)
	admitted, err := clientauthority.AdmitOwnerTransport(lifetime, host.runtime.UserBinding(), owner)
	if err != nil {
		cancel()
		return nil, err
	}
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	client := &PersonalChannelClient{host: host, ctx: admitted, cancel: cancel, input: writer, output: reader, done: make(chan struct{}), pending: map[string]chan personalChannelResponse{}}
	client.registry = expected
	go func() {
		defer close(client.done)
		err := host.Serve(admitted, input, output, nil)
		cancel()
		_ = input.CloseWithError(err)
		_ = output.CloseWithError(err)
	}()
	go client.read()
	return client, nil
}

// Call accepts native owner input only from the trusted adapter. In agents.send,
// request_id is the platform event identity and prompt is its unmodified text.
// Other methods reuse Personal's existing controls, receipts and source checks.
func (c *PersonalChannelClient) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := clientauthority.RequireOwner(c.ctx, c.host.runtime.UserBinding()); err != nil {
		return nil, err
	}
	proof, err := personalChannelInput(method, input)
	if err != nil {
		return nil, err
	}
	input, err = personalChannelPayload(input, proof)
	if err != nil {
		return nil, err
	}
	snapshot, release := c.host.runtime.RetainExtensionSnapshot()
	defer release()
	if snapshot.PluginHost == nil || snapshot.PluginHost.ServiceRegistry() == nil {
		return nil, errors.New("Personal service is unavailable")
	}
	if c.registry != nil && snapshot.PluginHost.ServiceRegistry() != c.registry {
		return nil, errors.New("Personal channel belongs to a retired provider generation")
	}
	provider, ok := snapshot.PluginHost.ServiceRegistry().ProviderPluginIDFor(personalActionsConsumerID, "personal.agents", 1)
	if !ok {
		return nil, errors.New("selected Personal service is unavailable")
	}
	c.next++
	id := strconv.FormatUint(c.next, 10)
	params := PluginClientRequestParams{GraphGeneration: snapshot.GenerationID, Method: method, Input: input, UserInput: proof, RequestID: "channel-" + id, UserSpaceID: c.host.runtime.UserBinding().UserSpaceID, ProductDomain: "personal"}
	for _, plugin := range snapshot.Plugins {
		if plugin.ID == provider {
			params.ID, params.Fingerprint = plugin.SubjectID, plugin.Fingerprint
			break
		}
	}
	if params.ID == "" {
		return nil, errors.New("selected Personal package is unavailable")
	}
	response := make(chan personalChannelResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = response
	c.pendingMu.Unlock()
	defer func() { c.pendingMu.Lock(); delete(c.pending, id); c.pendingMu.Unlock() }()
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	rawID, _ := json.Marshal(id)
	if err := json.NewEncoder(c.input).Encode(Request{ID: rawID, Method: MethodPluginClientRequest, Params: raw}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		// Cancel only this invocation. An already accepted send keeps its durable
		// receipt; the caller reconciles that same event identity on the next attempt.
		raw, _ := json.Marshal(PluginClientCancelParams{ID: params.ID, Fingerprint: params.Fingerprint, GraphGeneration: params.GraphGeneration, RequestID: params.RequestID})
		_ = json.NewEncoder(c.input).Encode(Request{ID: json.RawMessage(`"cancel"`), Method: MethodPluginClientCancel, Params: raw})
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	case reply := <-response:
		if reply.Error != nil {
			return nil, fmt.Errorf("%s: %s", reply.Error.Code, reply.Error.Message)
		}
		var result PluginClientRequestResult
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			return nil, err
		}
		return result.Result, nil
	}
}

func (c *PersonalChannelClient) read() {
	decoder := json.NewDecoder(c.output)
	for {
		var response personalChannelResponse
		if err := decoder.Decode(&response); err != nil {
			c.cancel()
			return
		}
		var id string
		if json.Unmarshal(response.ID, &id) != nil {
			continue
		}
		c.pendingMu.Lock()
		waiting := c.pending[id]
		if waiting != nil {
			delete(c.pending, id)
			waiting <- response
		}
		c.pendingMu.Unlock()
	}
}

func (c *PersonalChannelClient) Close() error {
	c.cancel()
	_ = c.input.Close()
	_ = c.output.Close()
	<-c.done
	return nil
}

func personalChannelInput(method string, raw json.RawMessage) (*runtime.ClientUserInput, error) {
	if method != "agents.send" && method != "agents.messages.resume" {
		return nil, nil
	}
	var input struct {
		runtime.ClientUserInput
		ReceiptID string `json:"receipt_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if method == "agents.messages.resume" {
		if input.ReceiptID == "" {
			return nil, errors.New("accepted message resume requires its original receipt")
		}
		input.ResumeReceiptID = input.ReceiptID
	}
	if err := runtime.ValidateClientUserInput(input.ClientUserInput); err != nil {
		return nil, err
	}
	return &input.ClientUserInput, nil
}

// The exact selected bytes stay in the native input proof; Personal receives
// only the references it can redeem against that proof through host.session.send.
func personalChannelPayload(raw json.RawMessage, proof *runtime.ClientUserInput) (json.RawMessage, error) {
	if proof == nil || len(proof.Attachments) == 0 {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	refs := make([]map[string]string, 0, len(proof.Attachments))
	for _, file := range proof.Attachments {
		refs = append(refs, map[string]string{"sha256": file.SHA256, "filename": file.Filename, "media_type": file.MediaType})
	}
	fields["attachments"], _ = json.Marshal(refs)
	return json.Marshal(fields)
}

type personalChannelResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *ResponseError  `json:"error"`
}
