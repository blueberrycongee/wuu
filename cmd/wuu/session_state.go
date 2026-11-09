package main

import (
	"context"
	"errors"
	"os"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/runtimeconfig"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// openCommandState uses the same configured composition as inference, without
// requiring a model or creating an inference runtime for a storage-only command.
func openCommandState(ctx context.Context, workdir string) (*runtime.State, error) {
	root, err := resolveWorkdir(workdir)
	if err != nil {
		return nil, err
	}
	home := os.Getenv("HOME")
	cfg, path, mode, err := runtimeconfig.Load(runtimeconfig.LoadOptions{RootDir: root, HomeDir: home})
	if errors.Is(err, config.ErrConfigNotFound) {
		cfg = runtimeconfig.StarterConfig()
		path, err = statepath.ConfigPath(home)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return runtime.OpenState(ctx, runtime.StateOptions{RootDir: root, HomeDir: home, ConfigPath: path, ConfigLoadMode: mode, Config: cfg})
}
