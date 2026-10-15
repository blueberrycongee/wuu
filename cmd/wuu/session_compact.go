package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/session"
)

func runSessionCompact(args []string) error {
	fs := flag.NewFlagSet("session compact", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	sessionsDir := fs.String("sessions-dir", "", "directory containing the sessions database; defaults to Wuu's sessions directory")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum compaction duration")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("session compact accepts no positional arguments and requires a positive timeout"))
	}
	dir := strings.TrimSpace(*sessionsDir)
	if dir == "" {
		var err error
		dir, err = resolveSessionsDir()
		if err != nil {
			return err
		}
	}
	interrupt, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupt, *timeout)
	defer cancel()
	result, err := session.CompactStorage(ctx, dir)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(result)
	}
	fmt.Printf("Sessions database: %d → %d allocated bytes; incremental reclamation enabled.\n", result.DatabaseBytesBefore, result.DatabaseBytesAfter)
	return nil
}
