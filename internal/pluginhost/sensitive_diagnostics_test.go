package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/providers"
	"os"
	"strings"
	"testing"
	"time"
)

const syntheticValidationSecret = "synthetic-unsaved-secret-do-not-retain"

func TestSensitiveModelValidationDiagnostics(t *testing.T) {
	if os.Getenv("WUU_SENSITIVE_HELPER") == "1" {
		runSensitiveDiagnosticHelper()
		return
	}
	client, err := Start(context.Background(), ProcessConfig{ID: "selected-external-model", Command: os.Args[0], Args: []string{"-test.run=^TestSensitiveModelValidationDiagnostics$"}, Env: map[string]string{"WUU_SENSITIVE_HELPER": "1"}, PluginRoot: t.TempDir(), ProjectRoot: t.TempDir(), WuuHome: t.TempDir(), Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	call := func(op string) error {
		input, _ := json.Marshal(map[string]any{"operation": op, "configuration": map[string]string{"api_key": syntheticValidationSecret}})
		_, err := client.InvokeCapability(ctx, CapabilityInvokeParams{Capability: CapabilityModelAdapter, Input: input})
		return err
	}
	concurrent := make(chan error, 1)
	go func() { concurrent <- call("hold") }()
	assertSanitized := func(err error) {
		t.Helper()
		if err == nil || strings.Contains(err.Error(), syntheticValidationSecret) {
			t.Fatalf("sensitive error not sanitized: %v", err)
		}
	}
	assertSanitized(call("validate"))
	select {
	case err := <-concurrent:
		assertSanitized(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertSanitized(call("late"))
	assertSanitized(call("malformed"))
	select {
	case <-client.readerDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for name, value := range map[string]string{"status": client.Status().Error, "reader": client.readFailure().Error(), "stderr": client.stderr.String()} {
		if strings.Contains(value, syntheticValidationSecret) {
			t.Fatalf("retained %s leaked candidate secret", name)
		}
	}
	if client.stderr.String() != "" {
		t.Fatal("sensitive process retained stderr")
	}
	if err := client.Close(context.Background()); err != nil && strings.Contains(err.Error(), syntheticValidationSecret) {
		t.Fatal("close diagnostic leaked")
	}
}

func runSensitiveDiagnosticHelper() {
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	held, validation := "", ""
	reject := func(id string) {
		_ = enc.Encode(map[string]any{"id": id, "error": rpcError{Code: syntheticValidationSecret, Message: syntheticValidationSecret}})
	}
	for scanner.Scan() {
		var request struct {
			ID, Method string
			Params     json.RawMessage
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return
		}
		switch request.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"id": request.ID, "result": CapabilityInitializeResult{ProtocolVersion: CapabilityProtocolVersion, Capabilities: []CapabilityDescriptor{{ID: CapabilityModelAdapter, Kind: SeamDecision, Version: 1}}, ModelAdapters: []ModelAdapterDescriptor{{ID: "fixture.model", Version: 1, Mode: "direct", Operations: []string{"validate"}}}}})
		case "capability.invoke":
			var params CapabilityInvokeParams
			_ = json.Unmarshal(request.Params, &params)
			var input struct{ Operation string }
			_ = json.Unmarshal(params.Input, &input)
			switch input.Operation {
			case "hold":
				held = request.ID
			case "validate":
				validation = request.ID
			case "late":
				fmt.Fprintln(os.Stderr, syntheticValidationSecret)
				reject(request.ID)
			case "malformed":
				fmt.Fprintln(os.Stderr, syntheticValidationSecret)
				fmt.Fprintln(os.Stdout, `{"id":{},"error":"`+syntheticValidationSecret+`"}`)
				return
			}
			if held != "" && validation != "" {
				fmt.Fprintln(os.Stderr, syntheticValidationSecret)
				reject(held)
				reject(validation)
				held, validation = "", ""
			}
		case "shutdown":
			_ = enc.Encode(map[string]any{"id": request.ID, "result": map[string]any{}})
			return
		}
	}
}

type sensitiveNativeFixture struct{ malformed bool }

func (*sensitiveNativeFixture) ID() string { return "arbitrary-selected-provider" }
func (*sensitiveNativeFixture) Status() Status {
	return Status{State: StateActive, Error: syntheticValidationSecret}
}
func (f *sensitiveNativeFixture) Close(context.Context) error {
	if f.malformed {
		panic(syntheticValidationSecret)
	}
	return errors.New(syntheticValidationSecret)
}
func (*sensitiveNativeFixture) ProtocolVersion() int { return CapabilityProtocolVersion }
func (*sensitiveNativeFixture) Capabilities() []CapabilityDescriptor {
	return []CapabilityDescriptor{{ID: CapabilityModelAdapter, Kind: SeamDecision, Version: 1}}
}
func (f *sensitiveNativeFixture) InvokeCapability(context.Context, CapabilityInvokeParams) (CapabilityInvokeResult, error) {
	if f.malformed {
		return CapabilityInvokeResult{Output: json.RawMessage(`{"` + syntheticValidationSecret + `":true}`)}, nil
	}
	return CapabilityInvokeResult{}, errors.New(syntheticValidationSecret)
}
func TestSensitiveValidationHostSanitizesNativeAndDecodeErrors(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		client := &sensitiveNativeFixture{malformed: malformed}
		host := New(client)
		host.recordDiagnostic(client.ID(), "prior", syntheticValidationSecret)
		capability, ok := host.Capability(client.ID(), CapabilityModelAdapter)
		if !ok {
			t.Fatal("missing selected capability")
		}
		var output struct{}
		err := host.InvokeCapability(context.Background(), capability, map[string]any{"operation": "validate", "configuration": map[string]string{"api_key": syntheticValidationSecret}}, &output)
		if err == nil || strings.Contains(err.Error(), syntheticValidationSecret) {
			t.Fatalf("host diagnostic leaked: %v", err)
		}
		host.recordDiagnostic(client.ID(), "late", syntheticValidationSecret)
		for _, status := range host.Statuses() {
			if strings.Contains(status.Error, syntheticValidationSecret) {
				t.Fatal("native status leaked")
			}
		}
		for _, diagnostic := range host.ContributionDiagnostics(client.ID()) {
			if strings.Contains(diagnostic.Message, syntheticValidationSecret) {
				t.Fatal("late contribution leaked")
			}
		}
		outcome, retired := host.RetirePlugin(context.Background(), client.ID(), errors.New("retire"))
		if !retired || outcome.Err == nil || strings.Contains(outcome.Err.Error(), syntheticValidationSecret) {
			t.Fatalf("retirement leaked: %v", outcome.Err)
		}
	}
}

func TestSensitiveDiagnosticsPreserveLocalCancellationAndScopeRejection(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, providers.ErrModelReceiptScopeClosed} {
		err := sanitizedSensitiveError(fmt.Errorf("%s: %w", syntheticValidationSecret, sentinel))
		if !errors.Is(err, sentinel) || strings.Contains(err.Error(), syntheticValidationSecret) {
			t.Fatalf("lost safe local semantics: %v", err)
		}
	}
}

func TestSensitiveStderrClearsAndDiscardsBufferedData(t *testing.T) {
	var buffer lockedBuffer
	_, _ = buffer.Write([]byte(syntheticValidationSecret))
	buffer.suppress()
	_, _ = buffer.Write([]byte(syntheticValidationSecret))
	if buffer.String() != "" || buffer.b.Len() != 0 {
		t.Fatal("sensitive stderr retained")
	}
}
