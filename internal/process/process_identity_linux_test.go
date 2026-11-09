//go:build linux

package process

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseLinuxProcessStartTicksHandlesParenthesesInCommand(t *testing.T) {
	stat := []byte("123 (worker ) name) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 424242 20")
	got, err := parseLinuxProcessStartTicks(stat)
	if err != nil {
		t.Fatal(err)
	}
	if got != 424242 {
		t.Fatalf("start ticks = %d, want 424242", got)
	}
}

func TestReadLinuxProcessIdentity(t *testing.T) {
	if os.Getenv("WUU_PROCESS_IDENTITY_HELPER") == "1" {
		select {}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadLinuxProcessIdentity$")
	cmd.Env = append(os.Environ(), "WUU_PROCESS_IDENTITY_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	identity, startedAt, precision, err := readProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(identity, "linux-v1:") {
		t.Fatalf("identity = %q, want linux-v1 prefix", identity)
	}
	if startedAt.IsZero() {
		t.Fatal("process start time is unavailable")
	}
	if precision <= time.Second {
		t.Fatalf("precision = %s, want Linux boot-time uncertainty", precision)
	}
}
