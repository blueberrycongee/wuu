package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
)

// SSH owns authentication, host-key verification, ProxyJump and agent forwarding
// policy. Wuu only forwards a loopback listener; it never copies credentials or
// runs a shell command assembled from the destination.
func runRemoteSSH(args []string) error {
	fs := flag.NewFlagSet("remote ssh", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	port := fs.Int("port", 8787, "local workspace port (use a different port for each host)")
	remotePort := fs.Int("remote-port", 0, "remote workspace port (default: same as --port)")
	sshPort := fs.Int("ssh-port", 0, "SSH port override (default: SSH config)")
	sshConfig := fs.String("ssh-config", "", "alternate OpenSSH configuration file")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: wuu remote ssh [--port 8787] [--ssh-port 22] user@host\nConnect to a running Wuu headless workspace host. SSH config aliases and ProxyJump are supported.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("one SSH destination is required")
	}
	destination := fs.Arg(0)
	if !regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.@:\[\]-]*$`).MatchString(destination) {
		return errors.New("invalid SSH destination; use user@host or an SSH config alias")
	}
	if *remotePort == 0 {
		*remotePort = *port
	}
	if *port < 1 || *port > 65535 || *remotePort < 1 || *remotePort > 65535 || *sshPort < 0 || *sshPort > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	forward := "127.0.0.1:" + strconv.Itoa(*port) + ":127.0.0.1:" + strconv.Itoa(*remotePort)
	// This invocation must own its foreground tunnel even when the user's SSH
	// configuration normally shares or backgrounds connections. Otherwise Ctrl-C
	// can leave forwarding attached to another master after this process exits.
	sshArgs := []string{"-N", "-T",
		"-o", "ControlMaster=no", "-o", "ControlPath=none",
		"-o", "ControlPersist=no", "-o", "ForkAfterAuthentication=no",
		"-o", "ExitOnForwardFailure=yes", "-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"-L", forward,
	}
	if *sshPort != 0 {
		sshArgs = append(sshArgs, "-p", strconv.Itoa(*sshPort))
	}
	if *sshConfig != "" {
		sshArgs = append(sshArgs, "-F", *sshConfig)
	}
	sshArgs = append(sshArgs, "--", destination)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cleanup := prepareRemoteSSHProcess(cmd)
	defer cleanup()
	fmt.Fprintf(os.Stdout, "Connecting to %s. This command forwards SSH traffic; it does not check workspace readiness.\nOnce the remote service reports ready, open http://127.0.0.1:%d/\nUse the pairing link printed by the remote host for the first connection.\nCtrl-C disconnects the tunnel; remote tasks keep running.\nIf SSH exits, rerun this command to reconnect. The remote host must already be running.\n", destination, *port)
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("SSH tunnel: %w", err)
	}
	return nil
}
