//go:build windows

package mcp

import (
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processGroup struct {
	job      windows.Handle
	assigned bool
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func configureProcessGroup(cmd *exec.Cmd) *processGroup {
	group := &processGroup{}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		slog.Warn("Windows MCP process job unavailable; falling back to direct process termination", "error", err)
		return group
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		slog.Warn("Windows MCP process job unavailable; falling back to direct process termination", "error", err)
		return group
	}
	// Keep the server suspended until it is assigned so early descendants inherit the job.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	group.job = job
	return group
}

func startProcessGroup(cmd *exec.Cmd, group *processGroup) error {
	if cmd.Process == nil {
		return fmt.Errorf("MCP server process was not started")
	}
	if group.job == 0 {
		return nil
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open suspended MCP server process: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(group.job, process); err != nil {
		if resumeErr := resumeProcess(process); resumeErr != nil {
			return fmt.Errorf("assign MCP server to process job: %w; resume suspended process: %v", err, resumeErr)
		}
		_ = closeProcessGroup(group)
		slog.Warn("Windows MCP process job assignment unavailable; falling back to direct process termination", "error", err)
		return nil
	}
	group.assigned = true
	if err := resumeProcess(process); err != nil {
		return fmt.Errorf("resume MCP server process: %w", err)
	}
	return nil
}

func resumeProcess(process windows.Handle) error {
	status, _, _ := ntResumeProcess.Call(uintptr(process))
	if int32(status) < 0 {
		return fmt.Errorf("NTSTATUS %#x", uint32(status))
	}
	return nil
}

func killProcessTree(cmd *exec.Cmd, group *processGroup) error {
	if group != nil && group.assigned {
		return windows.TerminateJobObject(group.job, 1)
	}
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func closeProcessGroup(group *processGroup) error {
	if group == nil || group.job == 0 {
		return nil
	}
	if err := windows.CloseHandle(group.job); err != nil {
		return err
	}
	group.job = 0
	group.assigned = false
	return nil
}
