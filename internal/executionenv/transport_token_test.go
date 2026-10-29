package executionenv

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/storelock"
	"github.com/stretchr/testify/require"
)

func TestTransportTokenConcurrentFirstConnectUsesOneSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-token")
	const count = 48
	values := make([]string, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range values {
		wg.Add(1)
		go func(i int) { defer wg.Done(); values[i], errs[i] = loadTransportToken(context.Background(), path) }(i)
	}
	wg.Wait()
	for i := range values {
		require.NoError(t, errs[i])
		require.Equal(t, values[0], values[i])
	}
	require.Len(t, values[0], 64)
}

func TestTransportTokenIncompleteFileFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-token")
	require.NoError(t, os.WriteFile(path, []byte("incomplete"), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := loadTransportToken(ctx, path)
	require.Error(t, err)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "incomplete", string(content))
}

func TestTransportTokenSubprocessHelper(t *testing.T) {
	path, ok := os.LookupEnv("WUU_TEST_TRANSPORT_TOKEN")
	if !ok {
		return
	}
	if err := os.WriteFile(os.Getenv("WUU_TEST_TRANSPORT_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("WUU_TEST_TRANSPORT_START")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(3)
		}
		time.Sleep(time.Millisecond)
	}
	token, err := loadTransportToken(context.Background(), path)
	if err != nil {
		os.Exit(4)
	}
	if err := os.WriteFile(os.Getenv("WUU_TEST_TRANSPORT_RESULT"), []byte(token), 0600); err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

func TestTransportTokenConcurrentHostProcessesUseOneSecret(t *testing.T) {
	dir := t.TempDir()
	path, start := filepath.Join(dir, "token"), filepath.Join(dir, "start")
	const count = 8
	commands := make([]*exec.Cmd, count)
	for i := range commands {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTransportTokenSubprocessHelper$")
		cmd.Env = append(os.Environ(), "WUU_TEST_TRANSPORT_TOKEN="+path, "WUU_TEST_TRANSPORT_START="+start, fmt.Sprintf("WUU_TEST_TRANSPORT_READY=%s/ready-%d", dir, i), fmt.Sprintf("WUU_TEST_TRANSPORT_RESULT=%s/result-%d", dir, i))
		require.NoError(t, cmd.Start())
		commands[i] = cmd
	}
	require.Eventually(t, func() bool {
		for i := range commands {
			if _, err := os.Stat(fmt.Sprintf("%s/ready-%d", dir, i)); err != nil {
				return false
			}
		}
		return true
	}, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, os.WriteFile(start, []byte("start"), 0600))
	var first string
	for i, cmd := range commands {
		require.NoError(t, cmd.Wait())
		value, err := os.ReadFile(fmt.Sprintf("%s/result-%d", dir, i))
		require.NoError(t, err)
		if i == 0 {
			first = string(value)
		}
		require.Equal(t, first, string(value))
	}
	require.Len(t, first, 64)
}

func TestTransportTokenWaitingHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	lock, err := storelock.AcquireContext(context.Background(), dir)
	require.NoError(t, err)
	defer lock.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = loadTransportToken(ctx, filepath.Join(dir, "token"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTransportTokenLockProcessHelper(t *testing.T) {
	dir, ok := os.LookupEnv("WUU_TEST_TRANSPORT_LOCK_DIR")
	if !ok {
		return
	}
	lock, err := storelock.AcquireContext(context.Background(), dir)
	if err != nil {
		os.Exit(2)
	}
	defer lock.Release()
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		os.Exit(3)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestTransportTokenOwnerCrashReleasesLock(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTransportTokenLockProcessHelper$")
	cmd.Env = append(os.Environ(), "WUU_TEST_TRANSPORT_LOCK_DIR="+dir)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(dir, "ready")); return err == nil }, 5*time.Second, 5*time.Millisecond)
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	token, err := loadTransportToken(ctx, filepath.Join(dir, "token"))
	require.NoError(t, err)
	require.Len(t, token, 64)
}
