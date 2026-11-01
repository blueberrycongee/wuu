package appserver

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/userspace"
	"sync/atomic"
	"testing"
	"time"
)

// Incoming text, reply identity and retry receipts must survive the channel
// adapter unchanged; unrelated owner controls must never mint user input.
func TestPersonalChannelInputProof(t *testing.T) {
	input := json.RawMessage(`{"agent_id":"main","request_id":"slack:event-1","prompt":"  exact text\n","relation":{"reply_to":{"session_id":"session","seq":7}}}`)
	proof, err := personalChannelInput("agents.send", input)
	if err != nil {
		t.Fatal(err)
	}
	if proof == nil || proof.RequestID != "slack:event-1" || proof.Prompt != "  exact text\n" {
		t.Fatalf("input changed: %+v", proof)
	}
	if proof.Relation == nil {
		t.Fatal("reply relation lost")
	}
	if _, err := personalChannelInput("agents.send", json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Fatal("missing stable request identity accepted")
	}
	if proof, err := personalChannelInput("tasks.stop", json.RawMessage(`{"task_id":"task"}`)); err != nil || proof != nil {
		t.Fatalf("control minted input: %+v %v", proof, err)
	}
}

func TestPersonalChannelAttachmentsKeepBytesOnlyInProof(t *testing.T) {
	raw := json.RawMessage(`{"agent_id":"main","request_id":"media-1","prompt":"read it","attachments":[{"sha256":"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824","filename":"note.txt","media_type":"text/plain","data":"aGVsbG8="}]}`)
	proof, err := personalChannelInput("agents.send", raw)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := personalChannelPayload(raw, proof)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof.Attachments) != 1 || proof.Attachments[0].Data != "aGVsbG8=" {
		t.Fatal("native attachment proof lost bytes")
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	var refs []map[string]json.RawMessage
	if err = json.Unmarshal(fields["attachments"], &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || len(refs[0]) != 3 || refs[0]["data"] != nil {
		t.Fatal("plugin received attachment bytes instead of references")
	}
}

// The transport goes through UserHost.Serve and the normal client RPC handler.
// A synthetic plugin verifies the exact host proof it receives; this is not a
// live Slack/Feishu or model acceptance test.
func TestPersonalChannelUsesAuthenticatedUserHostRPC(t *testing.T) {
	var plugin *channelProofPlugin
	srv, _, _ := newPluginStateTestServer(t, func(id string) pluginhost.Client {
		plugin = &channelProofPlugin{personalReceiptServiceClient: personalReceiptServiceClient{pluginClientRequestTestRuntime: pluginClientRequestTestRuntime{id: id}}}
		return plugin
	}, func(string) pluginhost.Client { return &channelProofConsumer{} })
	srv.rt.UserSpaceID, srv.rt.ProductDomain = "space", "personal"
	host := &UserHost{runtime: srv.rt, server: srv, connections: map[string]*userHostConnection{}, pending: map[string]userHostResponse{}}
	srv.out = host
	var live atomic.Bool
	live.Store(true)
	owner, err := principal.EnrolledOwner(principal.Identity{Kind: "user", Issuer: "wuu:im:slack", Tenant: "team", Subject: "user"}, "enrollment", live.Load, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewPersonalChannelClient(ctx, host, owner, host.runtime.ExtensionSnapshot().PluginHost.ServiceRegistry())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := client.Call(ctx, "agents.send", json.RawMessage(`{"agent_id":"main","request_id":"native-event","prompt":"exact native text"}`))
	if err != nil || string(raw) != `{"accepted":true}` {
		t.Fatalf("roundtrip: %s %v", raw, err)
	}
	if !plugin.sawProof.Load() {
		t.Fatal("native proof not checked by plugin invocation")
	}
	live.Store(false)
	if _, err = client.Call(ctx, "agents.send", json.RawMessage(`{"request_id":"revoked","prompt":"not admitted"}`)); err == nil {
		t.Fatal("revoked transport sent input")
	}
}

type channelProofPlugin struct {
	personalReceiptServiceClient
	sawProof atomic.Bool
}

func (p *channelProofPlugin) InvokeCapability(ctx context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	var input pluginhost.PluginClientRequestInput
	if err := json.Unmarshal(params.Input, &input); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	var body struct {
		RequestID string `json:"request_id"`
		Prompt    string `json:"prompt"`
	}
	if err := json.Unmarshal(input.Input, &body); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if err := runtime.AuthorizeClientUserInput(ctx, p.ID(), body.RequestID, "selected-session", body.Prompt, "host-send"); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	if err := clientauthority.RequireOwner(ctx, userspace.Binding{UserSpaceID: "space", ProductDomain: "personal"}); err != nil {
		return pluginhost.CapabilityInvokeResult{}, err
	}
	p.sawProof.Store(true)
	return pluginhost.CapabilityInvokeResult{Output: json.RawMessage(`{"result":{"accepted":true}}`)}, nil
}

type channelProofConsumer struct{ personalActionsConsumer }

func (*channelProofConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: "personal.agents", MajorVersion: 1, Required: true}}
}

func TestPersonalChannelResumeUsesOriginalReceiptProof(t *testing.T) {
	proof, err := personalChannelInput("agents.messages.resume", json.RawMessage(`{"agent_id":"main","request_id":"native-original","receipt_id":"accepted-original"}`))
	if err != nil || proof.RequestID != "native-original" || proof.ResumeReceiptID != "accepted-original" || proof.Prompt != "" {
		t.Fatalf("resume proof: %+v %v", proof, err)
	}
	if _, err = personalChannelInput("agents.messages.resume", json.RawMessage(`{"request_id":"native-original","receipt_id":"accepted-original","prompt":"replacement text"}`)); err == nil {
		t.Fatal("resume replaced the accepted text")
	}
}
