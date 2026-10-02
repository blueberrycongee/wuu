//go:build !windows

package executionworker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func socketPath(session string) (string, error) {
	if session == "" || filepath.Base(session) != session || session == "." || session == ".." {
		return "", errors.New("invalid execution session")
	}
	// Unix socket addresses have a short platform limit. Keep them independent
	// of the workspace and home path, inside a private per-user directory.
	dir := filepath.Join("/tmp", "wuu-execution-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) {
		return "", errors.New("execution socket directory is not private")
	}
	digest := sha256.Sum256([]byte(session))
	return filepath.Join(dir, fmt.Sprintf("worker-%x.sock", digest[:12])), nil
}

// Connect starts a detached session worker when necessary, then relays a private
// stream. A readiness pipe coordinates startup without timed sleeps.
func Connect(ctx context.Context, session string, idleSeconds int, in io.Reader, out io.Writer) error {
	path, err := socketPath(session)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		conn, err = net.Dial("unix", path)
		if err != nil {
			if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			readyRead, readyWrite, err := os.Pipe()
			if err != nil {
				return err
			}
			defer readyRead.Close()
			logFile, err := os.OpenFile(path+".log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				readyWrite.Close()
				return err
			}
			defer logFile.Close()
			cmd := exec.Command(executable, "execution-worker", "--socket", session, "--idle-seconds", strconv.Itoa(idleSeconds))
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			cmd.ExtraFiles = []*os.File{readyWrite}
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			if err = cmd.Start(); err != nil {
				readyWrite.Close()
				return err
			}
			readyWrite.Close()
			// The OS reaps the detached daemon; this short-lived connector owns no tools.
			_ = cmd.Process.Release()
			_ = readyRead.SetReadDeadline(time.Now().Add(10 * time.Second))
			var ready [1]byte
			if _, err = io.ReadFull(readyRead, ready[:]); err != nil {
				return fmt.Errorf("execution worker did not become ready: %w", err)
			}
			conn, err = net.Dial("unix", path)
			if err != nil {
				return err
			}
		}
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	defer conn.Close()
	go func() {
		_, _ = io.Copy(conn, in)
		if unixConn, ok := conn.(*net.UnixConn); ok {
			_ = unixConn.CloseWrite()
		}
	}()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	_, err = io.Copy(out, conn)
	return err
}

func ServeSocket(ctx context.Context, session string, idleSeconds int) error {
	path, err := socketPath(session)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	server := New()
	server.requireToken = true
	defer server.Close()
	if idleSeconds <= 0 {
		idleSeconds = 600
	}
	timeout := time.Duration(idleSeconds) * time.Second
	var mu sync.Mutex
	active := 0
	timer := time.AfterFunc(timeout, func() {
		mu.Lock()
		defer mu.Unlock()
		if active == 0 {
			_ = listener.Close()
		}
	})
	defer timer.Stop()
	ready := os.NewFile(3, "ready")
	if ready != nil {
		_, _ = ready.Write([]byte{1})
		_ = ready.Close()
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		mu.Lock()
		active++
		timer.Stop()
		mu.Unlock()
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer conn.Close()
			_ = serveConnection(ctx, conn, conn, server)
			mu.Lock()
			active--
			if active == 0 {
				timer.Reset(timeout)
			}
			mu.Unlock()
		}()
	}
}
