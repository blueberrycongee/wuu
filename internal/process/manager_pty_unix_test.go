//go:build !windows

package process

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

func openTestPTYSession(t *testing.T) (*ptySession, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	tty, err := preparePTYSession(master)
	if err != nil {
		_ = master.Close()
		_ = slave.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { tty.close(); _ = slave.Close() })
	cmd := exec.Command("stty", "raw", "-echo")
	cmd.Stdin = slave
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return tty, slave
}

func TestPTYOutputDrainsRetainedSlaveAfterResize(t *testing.T) {
	tty, slave := openTestPTYSession(t)
	if err := resizePTY(tty.master, 100, 40); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "output")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	want := []byte("final output\n")
	if _, err := slave.Write(want); err != nil {
		t.Fatal(err)
	}
	_ = tty.exitWrite.Close()
	done := make(chan struct{})
	go func() { copyPTYOutput(log, tty); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = slave.Close()
		<-done
		t.Fatal("retained slave prevented output drain after resize")
	}
	got, err := os.ReadFile(logPath)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("output = %q, error = %v", got, err)
	}
}

func TestPTYInputPreservesLargeWrites(t *testing.T) {
	tty, slave := openTestPTYSession(t)
	want := bytes.Repeat([]byte("input under backpressure\n"), 16384)
	got := make([]byte, len(want))
	readDone := make(chan error, 1)
	go func() { _, err := io.ReadFull(slave, got); readDone <- err }()
	writeDone := make(chan error, 1)
	go func() {
		n, err := tty.Write(want)
		if err == nil && n != len(want) {
			err = io.ErrShortWrite
		}
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY input stalled under backpressure")
	}
	select {
	case err := <-readDone:
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("PTY input changed: read error %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY input was not completely delivered")
	}
}

func TestPTYInputUnblocksOnLeaderExit(t *testing.T) {
	tty, _ := openTestPTYSession(t)
	want := bytes.Repeat([]byte("blocked input\n"), 65536)
	done := make(chan error, 1)
	go func() { _, err := tty.Write(want); done <- err }()
	// Completion closes all handles without waiting for an in-flight input
	// writer. Its poll must hold valid descriptors until the exit wake arrives.
	tty.close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected cancellation after leader exit, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("leader exit did not interrupt blocked PTY input")
	}
}

func TestPTYOutputReturnsWhileSlaveKeepsWriting(t *testing.T) {
	tty, slave := openTestPTYSession(t)
	log, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	started, writerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(writerDone)
		data := bytes.Repeat([]byte("ongoing output\n"), 32)
		_, err := slave.Write(data)
		close(started)
		for err == nil {
			_, err = slave.Write(data)
		}
	}()
	<-started
	_ = tty.exitWrite.Close()
	done := make(chan struct{})
	go func() { copyPTYOutput(log, tty); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		tty.close()
		<-done
		t.Fatal("continuously writing slave prevented completion")
	}
	tty.close()
	select {
	case <-writerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("slave writer did not stop after master close")
	}
}

func TestManagerTTYExitPublishesBufferedTail(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 8)
	m.Subscribe(events)
	defer m.Unsubscribe(events)
	p, err := m.Start(context.Background(), StartOptions{
		Command: "printf 'begin\\nFINAL_OUTPUT\\n'", OwnerKind: OwnerMainAgent, OwnerID: "test", TTY: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(p.ID)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != EventStopped || event.Process.ID != p.ID {
				continue
			}
			if event.Cause != EventCauseNaturalExit || event.Process.ExitCode != 0 {
				t.Fatalf("unexpected terminal event: %+v", event)
			}
			snapshot, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{})
			if err != nil || snapshot.Output != "begin\r\nFINAL_OUTPUT\r\n" {
				t.Fatalf("terminal event preceded complete output: %q, %v", snapshot.Output, err)
			}
			return
		case <-deadline:
			t.Fatal("natural PTY exit did not publish its terminal event")
		}
	}
}
