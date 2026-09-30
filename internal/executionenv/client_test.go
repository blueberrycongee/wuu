package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestTransportWorker(t *testing.T) {
	if os.Getenv("WUU_ENV_TEST_WORKER") != "1" {
		return
	}
	held := ""
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req Request
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		if req.Method == "run_code" && string(req.Data) == `{"hold":true}` {
			held = req.ID
			_ = json.NewEncoder(os.Stdout).Encode(Response{Method: "process", Data: json.RawMessage(`{"ready":true}`)})
			continue
		}
		if req.Method == "execute" && held != "" {
			_ = json.NewEncoder(os.Stdout).Encode(Response{ID: held, Error: "cancelled"})
			held = ""
		}
		if req.Method == "run_code" && held != "" {
			_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Error: "new program overtook cancelled execution"})
			continue
		}
		if req.Method == "disconnect" {
			os.Exit(3)
		}
		if req.Method == "wait" {
			continue
		}
		if req.Method == "cancel" {
			if req.ID == held {
				continue
			}
			_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Error: "cancelled"})
			continue
		}
		_ = json.NewEncoder(os.Stdout).Encode(Response{ID: req.ID, Data: json.RawMessage(`{"ok":true}`)})
	}
	os.Exit(0)
}

func testClient(t *testing.T) *Client {
	t.Helper()
	t.Setenv("WUU_ENV_TEST_WORKER", "1")
	c := NewClient([]string{os.Args[0], "-test.run=^TestTransportWorker$"}, []string{"WUU_ENV_TEST_WORKER=1"})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestTransportCancellationAndNoReplay(t *testing.T) {
	c := testClient(t)
	if _, err := c.Call(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "wait", nil); err == nil {
		t.Fatal("cancelled call succeeded")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, "wait", nil); err == nil {
		t.Fatal("deadline ignored")
	}
	if _, err := c.Call(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "disconnect", nil); err == nil {
		t.Fatal("connection loss succeeded")
	}
	if _, err := c.Call(context.Background(), "hello", nil); err == nil {
		t.Fatal("failed worker was automatically restarted")
	}
}

func TestConcurrentResponsesStayWithCaller(t *testing.T) {
	c := testClient(t)
	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			data, err := c.Call(context.Background(), "hello", nil)
			if err == nil && string(data) != `{"ok":true}` {
				err = fmt.Errorf("wrong result: %s", data)
			}
			done <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestProgramRetirementKeepsProcessControlAvailable(t *testing.T) {
	c := testClient(t)
	ready := make(chan struct{})
	c.event = func(json.RawMessage) { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "run_code", map[string]bool{"hold": true}); first <- err }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("program never started")
	}
	cancel()
	if err := <-first; err == nil {
		t.Fatal("cancelled program reported success")
	}
	nextCtx, nextCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer nextCancel()
	next := make(chan error, 1)
	go func() { _, err := c.Call(nextCtx, "run_code", nil); next <- err }()
	if _, err := c.Call(nextCtx, "execute", map[string]string{"action": "stop"}); err != nil {
		t.Fatalf("retirement blocked process control: %v", err)
	}
	if err := <-next; err != nil {
		t.Fatalf("program reuse: %v", err)
	}
}
