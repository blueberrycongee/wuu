package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/phone"
)

func TestNativeHostChild(t *testing.T) {
	if os.Getenv("WUU_NATIVE_HOST_HELPER") != "1" {
		return
	}
	main()
	os.Exit(0)
}

// Exercise the same disposable process/relay contract consumed by Swift, rather
// than replacing a runner after its selected model generation was captured.
func TestNativeHostSelectedModelRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeHostChild$")
	cmd.Env = append(os.Environ(), "WUU_NATIVE_HOST_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		fmt.Fprintln(stdin, "quit")
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Error(err)
		}
	}()
	var fixture map[string]string
	if err = json.NewDecoder(stdout).Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	credentials := &phone.Credentials{V: 1, DeviceSeed: fixture["deviceSeed"], HostPub: fixture["host"], RelayURL: "ws" + strings.TrimPrefix(fixture["server"], "http") + "/v1/connect"}
	connect := func() (*phone.Client, context.CancelFunc, <-chan struct{}) {
		client, err := phone.NewClient(credentials, phone.Options{})
		if err != nil {
			t.Fatal(err)
		}
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); _ = client.Run(runCtx) }()
		if err = client.WaitAttached(ctx); err != nil {
			stop()
			t.Fatal(err)
		}
		var initialized appserver.InitializeResult
		if err = client.Protocol().Call(ctx, appserver.MethodInitialize, nil, &initialized); err != nil {
			stop()
			t.Fatal(err)
		}
		return client, stop, done
	}
	client, stop, done := connect()
	defer func() { stop(); <-done }()
	var started appserver.ThreadStartResult
	if err = client.Protocol().Call(ctx, appserver.MethodThreadStart, appserver.ThreadStartParams{}, &started); err != nil {
		t.Fatal(err)
	}
	var turn appserver.TurnStartResult
	if err = client.Protocol().Call(ctx, appserver.MethodTurnStart, appserver.TurnStartParams{ThreadID: started.Thread.ID, Prompt: "native phone message", Images: []appserver.TurnStartImage{{MediaType: "image/png", Data: fixture["image"]}}, Files: []appserver.TurnStartFile{{MediaType: "application/pdf", Data: fixture["pdf"], Filename: "document.pdf"}}}, &turn); err != nil {
		t.Fatal(err)
	}
	// Snapshot while gated proves the selected endpoint did not complete early.
	var snapshot appserver.ThreadResumeResult
	if err = client.Protocol().Call(ctx, appserver.MethodThreadResume, appserver.ThreadResumeParams{SessionID: started.Thread.ID, ResponseOnly: true}, &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, turn := range snapshot.Thread.Turns {
		if turn.Status == appserver.TurnStatusFailed || turn.Status == appserver.TurnStatusCompleted {
			t.Fatalf("gated turn settled before release: %+v", turn)
		}
	}
	fmt.Fprintln(stdin, "release")
	completed := false
	for !completed {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case note := <-client.Protocol().Notifications():
			if note.Method == appserver.NotificationTurnError {
				var failure appserver.TurnErrorNotification
				_ = json.Unmarshal(note.Params, &failure)
				t.Fatalf("native fixture turn failed: %s", failure.Error)
			}
			if note.Method == appserver.NotificationTurnCompleted {
				completed = true
			}
		}
	}
	stop()
	<-done
	restored, closeRestored, restoredDone := connect()
	defer func() { closeRestored(); <-restoredDone }()
	if err = restored.Protocol().Call(ctx, appserver.MethodThreadResume, appserver.ThreadResumeParams{SessionID: started.Thread.ID, ResponseOnly: true}, &snapshot); err != nil {
		t.Fatal(err)
	}
	inputFound := false
	for _, turn := range snapshot.Thread.Turns {
		for _, item := range turn.Items {
			if item.Type == appserver.ThreadItemUserMessage && item.Text == "native phone message" {
				inputFound = true
				if len(item.Images) != 1 || len(item.Files) != 1 {
					t.Fatalf("reconnect attachment count: images=%d files=%d", len(item.Images), len(item.Files))
				}
			}
		}
	}
	if !inputFound {
		t.Fatal("reconnect lost user input")
	}
	raw, _ := json.Marshal(snapshot)
	if !strings.Contains(string(raw), "native transport verified") || !strings.Contains(string(raw), "document.pdf") {
		t.Fatal("reconnect lost completion or PDF attachment metadata")
	}
}
