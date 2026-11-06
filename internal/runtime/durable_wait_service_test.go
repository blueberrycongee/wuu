package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/durablewait"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

// Public routing must strip caller-authored approval authority, preserve the
// host's exact request attestation, and keep namespace state out of plugin writes.
func TestDurableWaitPublicAuthority(t *testing.T) {
	ctx := context.Background()
	registry := pluginhost.NewServiceRegistry()
	documents := persistence.NewService(persistence.SQLiteModule, t.TempDir(), "sqlite")
	client := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, durablewait.Module, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	waits := durablewait.NewService(client)
	consumer := &runtimeServiceConsumer{id: "driver", state: pluginhost.StatePrepared, requirements: pluginhost.KernelServiceRequirements(durablewait.ServiceName)}
	clients := []pluginhost.Client{documents, waits, consumer}
	host := pluginhost.New(clients...)
	conflicts := registry.RegisterClients(clients...)
	if len(conflicts) > 0 {
		t.Fatal(conflicts)
	}
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	call := func(method string, input durablewait.Request, attest bool) (json.RawMessage, error) {
		raw, _ := json.Marshal(input)
		callCtx := ctx
		if attest {
			sum := sha256.Sum256(raw)
			proof := pluginhost.ServiceAdmission{Version: 1, Principal: principal.LocalOwner().Identity(), Action: "durable-wait." + method, Authority: "test", InputDigest: hex.EncodeToString(sum[:])}
			callCtx = pluginhost.WithServiceAdmission(ctx, "driver", durablewait.ServiceName, method, proof)
		}
		result, err := host.CallService(callCtx, "driver", durablewait.ServiceName, method, raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	owner := durablewait.Owner{PluginID: "driver", SessionID: "session", ExecutionID: "execution", CallID: "approval"}
	register := durablewait.Request{Owner: owner, Fresh: true, Intent: durablewait.Intent{Version: 1, ID: "approval", Kind: "approval", Question: "Untrusted display text", BatchID: "batch", CallsDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}}
	if _, err := call("register", register, false); err == nil {
		t.Fatal("plugin minted pre-effect proof")
	}
	if _, err := call("register", register, true); err != nil {
		t.Fatal(err)
	}
	decide := durablewait.Request{SessionID: "session", WaitID: "approval", Decision: "approve"}
	if _, err := call("decide", decide, false); err == nil {
		t.Fatal("plugin self-approved")
	}
	wrong := decide
	wrong.SessionID = "other"
	if _, err := call("decide", wrong, true); err == nil {
		t.Fatal("cross-session approval")
	}
	if _, err := call("decide", decide, true); err != nil {
		t.Fatal(err)
	}
	dispatch := durablewait.Request{Owner: owner, WaitID: "approval", Intent: durablewait.Intent{BatchID: "batch", CallsDigest: "digest"}}
	if _, err := call("dispatch", dispatch, false); err == nil {
		t.Fatal("plugin bypassed gateway")
	}
	if _, err := call("dispatch", dispatch, true); err != nil {
		t.Fatal(err)
	}
	if _, err := call("dispatch", dispatch, true); err == nil {
		t.Fatal("duplicate effect admission")
	}
}
