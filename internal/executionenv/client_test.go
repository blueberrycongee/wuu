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
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req Request
		if json.Unmarshal(scan.Bytes(), &req) != nil {
			os.Exit(2)
		}
		if req.Method == "disconnect" {
			os.Exit(3)
		}
		if req.Method == "wait" {
			continue
		}
		if req.Method == "cancel" {
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
