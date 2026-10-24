package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/blueberrycongee/wuu/internal/composition"
)

// runComposition inspects bootstrap choices without activating any providers or
// reading the application home. Runtime availability is checked during startup.
func runComposition(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "resolve" {
		return errors.New("usage: wuu composition resolve [--file composition.json] [--profile NAME]")
	}
	fs := flag.NewFlagSet("composition resolve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("file", "", "standalone composition JSON file")
	profile := fs.String("profile", "", "profile to resolve (desktop or headless, or a configured profile)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("composition resolve does not accept positional arguments")
	}
	cfg := composition.DefaultConfig("desktop")
	if *path != "" {
		data, err := os.ReadFile(*path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, cfg); err != nil {
			return err
		}
	}
	if *profile != "" {
		cfg.Profile = *profile
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		return fmt.Errorf("resolve composition: %w", err)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(resolved)
}
