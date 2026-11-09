package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/runtimeconfig"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func runPersonalReviewMigration(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("state migrate-personal-review", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	space := fs.String("user-space", "", "explicit Personal user-space identity")
	configFile := fs.String("config", "", "same absolute config file used by the stopped host")
	profile := fs.String("composition-profile", "", "same explicit composition profile override used by the stopped host")
	apply := fs.Bool("apply", false, "import the inspected source into its empty selected review ledger")
	dryRun := fs.Bool("dry-run", false, "inspect only (the default); do not import review rows")
	expected := fs.String("expect-source-digest", "", "logical source SHA-256 returned by inspection; required for --apply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	binding := userspace.Binding{UserSpaceID: strings.TrimSpace(*space), ProductDomain: "personal"}
	if fs.NArg() != 0 || !binding.IsUser() || binding.Validate() != nil {
		return errors.New("review migration requires --user-space and accepts no positional arguments")
	}
	if *apply && *dryRun {
		return errors.New("choose --apply or --dry-run, not both")
	}
	if *apply && strings.TrimSpace(*expected) == "" {
		return errors.New("--apply requires --expect-source-digest from a prior inspection")
	}
	if !*apply && strings.TrimSpace(*expected) != "" {
		return errors.New("--expect-source-digest is only accepted with --apply")
	}
	homeDir := os.Getenv("HOME")
	path := strings.TrimSpace(*configFile)
	var err error
	if path == "" {
		path, err = statepath.ConfigPath(homeDir)
		if err != nil {
			return err
		}
	}
	if !filepath.IsAbs(path) {
		return errors.New("review migration requires an absolute config path")
	}
	cfg, path, err := config.LoadPath(path)
	if err != nil {
		return err
	}
	overrides := runtimeconfig.Overrides{CompositionProfile: strings.TrimSpace(*profile)}
	if err := runtimeconfig.ApplyOverrides(&cfg, overrides); err != nil {
		return err
	}
	bindings := appserver.NewRuntimeBindings()
	// The command itself is trusted local ingress. No JSON owner flag, remote
	// identity claim, business task, or restored approval can create this context.
	ctx, cancel := context.WithCancel(principal.WithContext(context.Background(), principal.LocalOwner()))
	defer cancel()
	result, err := appserver.RunPersonalReviewMigration(ctx, runtime.Options{
		UserSpaceID: binding.UserSpaceID, ProductDomain: binding.ProductDomain,
		HomeDir: homeDir, ConfigPath: path, ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg, Overrides: overrides,
		NativeProviders: bindings.NativeProviders(), NonInteractive: true,
	}, appserver.PersonalReviewMigrationRequest{Apply: *apply, ExpectedSourceDigest: strings.TrimSpace(*expected)})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
