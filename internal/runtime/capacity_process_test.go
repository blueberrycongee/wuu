package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSelectedCapacityProcessSharesPhysicalOwner(t *testing.T) {
	fixture := os.Getenv("WUU_CAPACITY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/capacity-provider-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	shared := NewCapacityResources(1)
	t.Cleanup(func() {
		if err := shared.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	hosts := make([]*pluginhost.Host, 2)
	for i := range hosts {
		root := t.TempDir()
		home, state := testWorkspaceState(t, root)
		plugin := testRuntimePlugin("independent-capacity")
		plugin.Root = fixture
		plugin.Runtime.Command = node
		plugin.Runtime.Args = []string{"capacity.ts"}
		selection := &composition.Config{Version: composition.Version, Services: []composition.Binding{{Service: capacityPolicyService, Major: 1, Provider: plugin.ID}}}
		host, _, err := buildPluginHost([]pluginpkg.Plugin{plugin}, root, "capacity", home, state, nil, startPluginClient, nil, nil, selection, "", withCapacityProviders(nil, shared)...)
		if err != nil {
			t.Fatal(err)
		}
		if err := host.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = host.Close(context.Background()) })
		hosts[i] = host
	}
	scope, err := hosts[0].OpenServiceScope(context.Background(), modelExecutionConsumer, capacityPolicyService, capacityResourceService)
	if err != nil {
		t.Fatal(err)
	}
	first, err := acquireModelCapacity(scope.Context, hosts[0], "common-account", providers.InferenceAttempt{}, "provider:route")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if extra, err := acquireModelCapacity(ctx, hosts[1], "common-account", providers.InferenceAttempt{}, "provider:route"); err == nil {
		_ = extra.settle("abandoned", nil)
		t.Fatal("external strategy bypassed shared physical quota")
	}
	cancel()
	if err := first.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireModelCapacity(context.Background(), hosts[1], "common-account", providers.InferenceAttempt{}, "provider:route")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.settle("succeeded", nil); err != nil {
		t.Fatal(err)
	}
}

// A physical receipt and its RPC tail have distinct lifetimes: cancellation
// does not free a physical slot, settlement does, and only RPC completion (or
// confirmed process termination) releases the retained callback authority.
func TestSelectedCapacityProcessCanceledReceiptTail(t *testing.T) {
	fixture := os.Getenv("WUU_CAPACITY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/capacity-provider-e2e.mjs")
	}
	for _, terminal := range []string{"settle", "kill"} {
		t.Run(terminal, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			shared := NewCapacityResources(1)
			t.Cleanup(func() {
				if err := shared.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			hosts := make([]*pluginhost.Host, 2)
			for i := range hosts {
				root := t.TempDir()
				home, state := testWorkspaceState(t, root)
				plugin := testRuntimePlugin("independent-receipt")
				plugin.Root = fixture
				plugin.Runtime.Command, err = exec.LookPath("node")
				if err != nil {
					t.Fatal(err)
				}
				plugin.Runtime.Args = []string{"receipt.ts", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)}
				modules := append(testStateModules(), kernelHostModule, modelExecutionConsumer, capacityPolicyModule, capacityResourceModule, plugin.ID)
				host, kernel, err := buildPluginHost([]pluginpkg.Plugin{plugin}, root, "", home, state, nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "", withCapacityProviders(nil, shared)...)
				if err != nil {
					t.Fatal(err)
				}
				kernel.bindModelSession(&Session{RootDir: root, HomeDir: home}, host)
				if err := host.Activate(context.Background()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = host.Close(context.Background()) })
				hosts[i] = host
			}
			modelCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, receipts := providers.NewModelReceiptScope(modelCtx)
			req, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Provider: "fixture", Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "hold"}}}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := (&modelAdapterClient{host: hosts[0]}).StreamChat(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			scanner := bufio.NewScanner(conn)
			read := func(want string) {
				t.Helper()
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				if !scanner.Scan() || scanner.Text() != want {
					t.Fatalf("control wanted %q, got %q (%v)", want, scanner.Text(), scanner.Err())
				}
			}
			read("admitted")
			if terminal == "settle" {
				retirementCtx, stopRetirement := context.WithTimeout(context.Background(), 100*time.Millisecond)
				outcome, found := hosts[0].RetirePlugin(retirementCtx, "independent-receipt", errors.New("disabled"))
				stopRetirement()
				if !found || outcome.Err == nil {
					t.Fatalf("active retirement must report pending cleanup: %+v, found=%v", outcome, found)
				}
			}
			cancel()
			receipts.Seal()
			read("canceled")
			logicalDone := make(chan struct{})
			go func() {
				for range stream {
				}
				close(logicalDone)
			}()
			select {
			case <-logicalDone:
			case <-time.After(time.Second):
				t.Fatal("logical cancellation waited for physical tail")
			}
			select {
			case <-receipts.Done():
				t.Fatal("receipt authority ended before physical tail")
			default:
			}
			blocked, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
			extra, err := acquireModelCapacity(blocked, hosts[1], "common-account", providers.InferenceAttempt{}, "provider:fixture")
			stop()
			if err == nil {
				_ = extra.settle("abandoned", nil)
				t.Fatal("logical cancellation freed unsettled physical capacity")
			}
			if terminal == "settle" {
				if _, err := fmt.Fprintln(conn, "settle"); err != nil {
					t.Fatal(err)
				}
				read("settled")
				select {
				case <-receipts.Done():
					t.Fatal("settlement incorrectly ended RPC receipt scope")
				default:
				}
				nextCtx, nextCancel := context.WithTimeout(context.Background(), time.Second)
				next, err := acquireModelCapacity(nextCtx, hosts[1], "common-account", providers.InferenceAttempt{}, "provider:fixture")
				nextCancel()
				if err != nil {
					t.Fatalf("settled physical slot remained held by RPC tail: %v", err)
				}
				if err := next.settle("succeeded", nil); err != nil {
					t.Fatal(err)
				}
				if _, err := fmt.Fprintln(conn, "return"); err != nil {
					t.Fatal(err)
				}
			}
			// In the unacknowledged branch the process drain deadline must kill/join
			// the peer before the abandoned slot can be released.
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			if scanner.Scan() || scanner.Err() != nil {
				t.Fatalf("expected physical peer EOF, got %q (%v)", scanner.Text(), scanner.Err())
			}
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer waitCancel()
			_ = receipts.Wait(waitCtx) // cancellation/confirmed kill is an expected receipt error
			select {
			case <-receipts.Done():
			case <-waitCtx.Done():
				t.Fatal("physical tail did not drain")
			}
			next, err := acquireModelCapacity(waitCtx, hosts[1], "common-account", providers.InferenceAttempt{}, "provider:fixture")
			if err != nil {
				t.Fatal(err)
			}
			if err := next.settle("succeeded", nil); err != nil {
				t.Fatal(err)
			}
			submissions := req.Attempt.SubmissionSnapshot()
			if len(submissions) != 1 {
				t.Fatalf("physical submissions=%+v", submissions)
			}
			if terminal == "settle" && (submissions[0].Outcome != providers.InferenceSubmissionSucceeded || submissions[0].ReportedUsage == nil || submissions[0].ReportedUsage.InputTokens != 7 || submissions[0].ReportedUsage.OutputTokens != 4) {
				t.Fatalf("late settlement lost: %+v", submissions[0])
			}
			if terminal == "kill" && submissions[0].Outcome == providers.InferenceSubmissionSucceeded {
				t.Fatal("unacknowledged kill fabricated success")
			}
		})
	}
}
