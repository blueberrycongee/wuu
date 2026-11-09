//go:build windows

package session

import (
	"os"
	"os/exec"
	"testing"
)

func TestInferenceRuntimeProcessAliveWindows(t *testing.T) {
	if inferenceRuntimeProcessAlive(0) {
		t.Fatal("pid 0 must not be treated as a live inference runtime")
	}
	if !inferenceRuntimeProcessAlive(os.Getpid()) {
		t.Fatal("current process must be treated as live")
	}

	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if inferenceRuntimeProcessAlive(cmd.Process.Pid) {
		t.Fatalf("exited process %d must not be treated as live", cmd.Process.Pid)
	}
}
