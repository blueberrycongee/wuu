// Command wuu-server runs the self-hosted account and connection service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/blueberrycongee/wuu/internal/remote/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	args := append([]string{"--state", "./data/relay.json"}, os.Args[1:]...)
	if err := server.RunStandalone(ctx, args, os.Stdout); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
