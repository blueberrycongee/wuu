package hooks

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// The child keeps inherited stdout/stderr open until killed or released by the
// test. A socket handshake makes cancellation independent of process startup.
func TestCommandHookProcessHelper(t *testing.T) {
	if stream := os.Getenv("WUU_HOOK_OUTPUT_TEST_STREAM"); stream != "" {
		file := os.Stdout
		if stream == "stderr" {
			file = os.Stderr
		}
		_, _ = io.WriteString(file, `{}`+strings.Repeat(" ", (1<<20)+1))
		os.Exit(0)
	}
	addr := os.Getenv("WUU_HOOK_PROCESS_TEST_ADDR")
	if addr == "" {
		return
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, conn)
	_ = conn.Close()
	os.Exit(0)
}

func TestCommandHookRejectsOversizedOutput(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			t.Setenv("WUU_HOOK_OUTPUT_TEST_STREAM", stream)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			h := &CommandHook{Command: "'" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestCommandHookProcessHelper$"}
			out, err := h.Execute(context.Background(), &Input{})
			if err == nil || out != nil || IsBlocked(err) {
				t.Fatalf("oversized output must fail without a partial decision: out=%+v err=%v", out, err)
			}
		})
	}
}

func TestCommandHookStopsDescendants(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
			t.Setenv("WUU_HOOK_PROCESS_TEST_ADDR", listener.Addr().String())
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := &CommandHook{
				Command: "'" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestCommandHookProcessHelper$ & wait",
				Timeout: time.Second,
			}
			done := make(chan error, 1)
			go func() {
				_, err := h.Execute(ctx, &Input{Event: PreToolUse})
				done <- err
			}()
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) || IsBlocked(err) {
					t.Errorf("hook error = %v, want %v (not a block)", err, want)
				}
			case <-time.After(4 * time.Second):
				_ = conn.Close() // Release the helper even on a broken implementation.
				<-done
				t.Fatal("hook waited for its descendant after cancellation")
			}
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var b [1]byte
			if _, err := conn.Read(b[:]); err == nil {
				t.Fatal("unexpected child output")
			} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("hook returned but left its descendant running")
			}
		})
	}
}
