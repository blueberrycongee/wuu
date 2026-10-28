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
	"github.com/blueberrycongee/wuu/internal/persistence"
)

func runSessionCompact(args []string) (err error) {
	fs := flag.NewFlagSet("session compact", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	sessionsDir := fs.String("sessions-dir", "", "legacy path override; configure the state.documents provider instead")
	workdir := fs.String("workdir", "", "workspace directory for the selected composition")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum compaction duration")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("session compact accepts no positional arguments and requires a positive timeout"))
	}
	if strings.TrimSpace(*sessionsDir) != "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("--sessions-dir cannot select a provider; configure state.documents and use --workdir"))
	}
	interrupt, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(interrupt, *timeout)
	defer cancel()
	state, err := openCommandState(ctx, *workdir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, state.Close()) }()
	compactor, ok := state.Documents.(persistence.Compactor)
	if !ok {
		return errors.New("selected document provider does not support physical compaction")
	}
	result, err := compactor.Compact(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(result)
	}
	fmt.Printf("Document storage: %d → %d file bytes; revision %d preserved.\n", result.BytesBefore, result.BytesAfter, result.Revision)
	return nil
}
