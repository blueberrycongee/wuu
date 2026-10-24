package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

func runState(args []string) error {
	if len(args) == 0 {
		return errors.New("state requires check, export, import, or migrate")
	}
	fs := flag.NewFlagSet("state "+args[0], flag.ContinueOnError)
	dir := fs.String("dir", "", "document store directory")
	file := fs.String("file", "", "portable snapshot file for export or import")
	backend := fs.String("backend", "", "backend for import: sqlite or jsonl")
	from := fs.String("from", "", "current backend for migration")
	to := fs.String("to", "", "target backend for migration")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*dir) == "" || fs.NArg() != 0 {
		return errors.New("state requires --dir and accepts no positional arguments")
	}
	ctx := context.Background()
	switch args[0] {
	case "check":
		info, _, err := persistence.Inspect(ctx, *dir)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(info)
	case "export":
		if *file == "" {
			return errors.New("export requires --file")
		}
		_, snapshot, err := persistence.Inspect(ctx, *dir)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		return securefs.WriteFileAtomic(*file, append(raw, '\n'))
	case "import":
		if *file == "" || *backend == "" {
			return errors.New("import requires --file and --backend")
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		decoder := json.NewDecoder(input)
		decoder.DisallowUnknownFields()
		var snapshot persistence.Snapshot
		if err := decoder.Decode(&snapshot); err != nil {
			return fmt.Errorf("decode snapshot: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return errors.New("snapshot must contain one JSON object")
		}
		return persistence.Import(ctx, *dir, *backend, snapshot)
	case "migrate":
		if *from == "" || *to == "" {
			return errors.New("migrate requires --from and --to; stop business consumers before switching their provider")
		}
		return persistence.Migrate(ctx, *dir, *from, *to)
	default:
		return fmt.Errorf("unknown state command %q", args[0])
	}
}
