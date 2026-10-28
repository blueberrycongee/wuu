package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/version"
	pluginsdk "github.com/blueberrycongee/wuu/packages/plugin-sdk"
)

func exportPluginTypes(target string) error {
	// Exclusive creation protects any edited source in a previous export.
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	if err := os.CopyFS(target, pluginsdk.Sources); err != nil {
		return err
	}
	// Source exports let ordinary TypeScript and bundler resolution consume the
	// exact host SDK without an unpublished npm package or a source checkout.
	return os.WriteFile(filepath.Join(target, "package.json"), []byte(`{
  "name": "@wuu/plugin-sdk",
  "private": true,
  "type": "module",
  "exports": { ".": "./src/index.ts", "./ui": "./src/ui.ts" }
}
`), 0600)
}

// Describe captures the same configured generation used by CLI state commands.
// It starts no inference; installed trusted providers may activate normally.
func runPluginDescribe(args []string) (err error) {
	fs := flag.NewFlagSet("plugin describe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	workdir := fs.String("workdir", "", "project whose composition to inspect")
	types := fs.String("types-dir", "", "new directory for the exact embedded SDK source and types")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("plugin describe takes no positional arguments")
	}
	if *types != "" {
		if err := exportPluginTypes(*types); err != nil {
			return err
		}
	}
	binding, err := openCommandState(context.Background(), *workdir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, binding.Close()) }()
	if binding.Host == nil || binding.Host.ServiceRegistry() == nil {
		return errors.New("selected composition has no authoritative service registry")
	}
	packages := binding.Generation.ActivePackages()
	sources := make([]map[string]any, 0, len(packages))
	for _, item := range packages {
		sources = append(sources, map[string]any{"id": item.ID, "version": item.Version, "fingerprint": item.Fingerprint, "root": item.Root})
	}
	data, err := json.MarshalIndent(map[string]any{"version": version.Info(), "scope": "new CLI state generation (not an attached desktop session)", "services": binding.Host.ServiceRegistry().Snapshot(0), "modules": binding.Host.Statuses(), "tools": binding.Host.ToolDefinitions(), "packages": sources, "sdk_source": *types}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
