package pluginhost

import (
	"bufio"
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/providers"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelCapabilityCancellationRetainsPhysicalDispatch(t *testing.T) {
	for _, mode := range []string{"response", "terminated"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_ = listener.SetDeadline(time.Now().Add(6 * time.Second))
			root := t.TempDir()
			client, err := Start(context.Background(), ProcessConfig{ID: "model-drain", Command: os.Args[0], Args: []string{"-test.run=^TestProcessClientReceiptCompletion$"}, Env: map[string]string{"WUU_PLUGINHOST_RECEIPT_HELPER": "1", "WUU_PLUGINHOST_RECEIPT_USAGE": "1", "WUU_PLUGINHOST_RECEIPT_ADDR": listener.Addr().String()}, PluginRoot: root, ProjectRoot: filepath.Dir(root), WuuHome: t.TempDir(), Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close(context.Background())
			control, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			_ = control.SetDeadline(time.Now().Add(6 * time.Second))
			reader := bufio.NewReader(control)
			expect := func(want string) {
				t.Helper()
				got, err := reader.ReadString('\n')
				if err != nil || got != want+"\n" {
					t.Fatalf("gate %q %v want %q", got, err, want)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, receipts := providers.NewModelReceiptScope(ctx)
			returned := make(chan error, 1)
			var lateResult CapabilityInvokeResult
			go func() {
				result, err := client.InvokeCapability(ctx, CapabilityInvokeParams{Capability: CapabilityModelAdapter, ExecutionID: "physical", Input: mustRaw("hold")})
				lateResult = result
				returned <- err
			}()
			expect("invoked")
			// Child receipt can race the host write returning. This test covers
			// cancellation after dispatch, not uncertain partial-write retirement.
			client.writeGate <- struct{}{}
			<-client.writeGate
			cancel()
			expect("canceled")
			select {
			case <-returned:
				t.Fatal("physical dispatch ended on logical cancellation")
			default:
			}
			if mode == "response" {
				_, _ = io.WriteString(control, "release\n")
			}
			select {
			case err := <-returned:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost logical cancellation: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("physical dispatch did not drain or terminate")
			}
			receipts.Seal()
			wait, cancelWait := context.WithTimeout(context.Background(), time.Second)
			defer cancelWait()
			receiptErr := receipts.Wait(wait)
			if mode == "response" && receiptErr != nil {
				t.Fatal(receiptErr)
			}
			if mode == "response" && !strings.Contains(string(lateResult.Output), `"input_tokens":7`) {
				t.Fatal("late physical usage was discarded")
			}
			if mode == "terminated" {
				if receiptErr == nil {
					t.Fatal("lost forced termination uncertainty")
				}
				client.stopMu.Lock()
				stopped := client.stopped
				client.stopMu.Unlock()
				if !stopped || client.cmd.ProcessState == nil {
					t.Fatal("released callback before confirmed process termination")
				}
			}
		})
	}
}
