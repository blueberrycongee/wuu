package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/version"
)

// runPluginDev dispatches to the plugin developer subcommands.
func runPluginDev(args []string) error {
	if len(args) == 0 {
		return pluginCLIError(errors.New("plugin dev subcommand is required (available: create, validate, build, test, pack, dev)"))
	}
	switch args[0] {
	case "create":
		return runPluginCreate(args[1:])
	case "validate":
		return runPluginValidate(args[1:])
	case "build":
		return runPluginBuild(args[1:])
	case "test":
		return runPluginTest(args[1:])
	case "pack":
		return runPluginPack(args[1:])
	case "dev":
		return runPluginDevMode(args[1:])
	default:
		return pluginCLIError(fmt.Errorf("unknown plugin dev subcommand %q (available: create, validate, build, test, pack, dev)", args[0]))
	}
}

// runPluginCreate scaffolds a new plugin directory.
func runPluginCreate(args []string) error {
	fs := flag.NewFlagSet("plugin create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "", "Output directory (defaults to ./<name>)")
	pluginType := fs.String("type", "agent", "Plugin type: agent, desktop, or full")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() != 1 {
		return pluginCLIError(errors.New("plugin create requires a plugin name"))
	}
	name := fs.Arg(0)

	if err := validatePluginName(name); err != nil {
		return pluginCLIError(err)
	}

	typeName := strings.TrimSpace(*pluginType)
	switch typeName {
	case "agent", "desktop", "full":
	default:
		return pluginCLIError(fmt.Errorf("unknown plugin type %q (available: agent, desktop, full)", typeName))
	}

	dir := strings.TrimSpace(*output)
	if dir == "" {
		dir = name
	}

	if _, err := os.Lstat(dir); err == nil {
		return pluginCLIError(fmt.Errorf("output path already exists: %s", dir))
	} else if !os.IsNotExist(err) {
		return pluginCLIError(fmt.Errorf("inspect output path: %w", err))
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return pluginCLIError(fmt.Errorf("create output parent: %w", err))
	}
	staging, err := os.MkdirTemp(parent, ".wuu-plugin-create-")
	if err != nil {
		return pluginCLIError(fmt.Errorf("create staging directory: %w", err))
	}
	defer os.RemoveAll(staging)

	manifest := map[string]any{
		"schema_version": 1,
		"id":             name,
		"name":           name,
		"version":        "0.1.0",
		"description":    fmt.Sprintf("A Wuu plugin: %s", name),
	}

	switch typeName {
	case "agent":
		manifest["runtime"] = map[string]any{
			"protocol": "wuu-plugin-v1",
			"command":  "node",
			"args":     []string{"dist/index.js"},
		}
	case "desktop":
		manifest["desktop"] = map[string]any{"entry": "dist/index.js"}
	case "full":
		manifest["runtime"] = map[string]any{
			"protocol": "wuu-plugin-v1",
			"command":  "node",
			"args":     []string{"dist/runtime.js"},
		}
		manifest["desktop"] = map[string]any{"entry": "dist/renderer.js"}
	}

	manifestPath := filepath.Join(staging, "plugin.json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return pluginCLIError(fmt.Errorf("marshal manifest: %w", err))
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return pluginCLIError(fmt.Errorf("write manifest: %w", err))
	}

	srcDir := filepath.Join(staging, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return pluginCLIError(fmt.Errorf("create src: %w", err))
	}

	for relative, content := range pluginScaffoldSources(typeName) {
		if err := os.WriteFile(filepath.Join(srcDir, relative), []byte(content), 0o644); err != nil {
			return pluginCLIError(fmt.Errorf("write %s: %w", relative, err))
		}
	}

	pkgPath := filepath.Join(staging, "package.json")
	if err := os.WriteFile(pkgPath, []byte(pluginScaffoldPackageJSON(name)), 0o644); err != nil {
		return pluginCLIError(fmt.Errorf("write package.json: %w", err))
	}

	tsconfigPath := filepath.Join(staging, "tsconfig.json")
	if err := os.WriteFile(tsconfigPath, []byte(pluginScaffoldTSConfig()), 0o644); err != nil {
		return pluginCLIError(fmt.Errorf("write tsconfig.json: %w", err))
	}

	if err := os.Rename(staging, dir); err != nil {
		return pluginCLIError(fmt.Errorf("publish plugin scaffold: %w", err))
	}
	fmt.Printf("Created plugin %q in %s\n", name, dir)
	fmt.Printf("  manifest: %s\n", filepath.Join(dir, "plugin.json"))
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  cd %s\n", dir)
	fmt.Printf("  npm install\n")
	fmt.Printf("  npm run build\n")
	fmt.Printf("  wuu plugin dev .\n")
	return nil
}

func runPluginValidate(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin validate")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() == 0 {
		return pluginCLIError(errors.New("plugin validate requires a plugin directory path"))
	}
	source := fs.Arg(0)
	prepared, cleanup, err := preparePluginSource(source)
	if err != nil {
		return pluginCLIError(fmt.Errorf("validation failed: %w", err))
	}
	defer cleanup()
	inspection, err := pluginpkg.InspectPackage(prepared)
	if err != nil {
		return pluginCLIError(fmt.Errorf("validation failed: %w", err))
	}
	manifest, err := pluginpkg.LoadManifest(filepath.Join(prepared, "plugin.json"), "validate")
	if err != nil {
		return pluginCLIError(fmt.Errorf("validation failed: %w", err))
	}
	if err := pluginpkg.CheckMinimumWuuVersion(manifest.MinimumWuuVersion, version.Info().Version); err != nil {
		return pluginCLIError(fmt.Errorf("validation failed: %w", err))
	}

	output := packageInspectionOutput(inspection)
	if *jsonOutput {
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("✓ Plugin %s v%s is valid\n", inspection.ID, inspection.Version)
	fmt.Printf("  fingerprint: %s\n", inspection.Fingerprint)
	fmt.Printf("  files:       %d (%d bytes)\n", inspection.FileCount, inspection.UnpackedSize)
	if len(inspection.RequestedPermissions) > 0 {
		fmt.Printf("  permissions: %s\n", strings.Join(inspection.RequestedPermissions, ", "))
	}
	if len(inspection.UnsupportedFields) > 0 {
		fmt.Printf("  ⚠ unsupported fields: %s\n", strings.Join(inspection.UnsupportedFields, ", "))
	}
	return nil
}

func runPluginBuild(args []string) error {
	fs := flag.NewFlagSet("plugin build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	packageManager := fs.String("package-manager", "", "Package manager executable (defaults from packageManager or lockfile)")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() != 1 {
		return pluginCLIError(errors.New("plugin build requires a plugin directory path"))
	}
	source := fs.Arg(0)
	if err := executePluginBuild(context.Background(), source, strings.TrimSpace(*packageManager)); err != nil {
		return pluginCLIError(fmt.Errorf("build failed: %w", err))
	}
	prepared, cleanup, err := preparePluginSource(source)
	if err != nil {
		return pluginCLIError(fmt.Errorf("build output validation failed: %w", err))
	}
	defer cleanup()
	inspection, err := pluginpkg.InspectPackage(prepared)
	if err != nil {
		return pluginCLIError(fmt.Errorf("build output validation failed: %w", err))
	}
	fmt.Printf("Built and validated %s v%s\n", inspection.ID, inspection.Version)
	return nil
}

type pluginProjectPackage struct {
	Scripts        map[string]string `json:"scripts"`
	PackageManager string            `json:"packageManager"`
}

func executePluginBuild(ctx context.Context, source, packageManagerOverride string) error {
	abs, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(abs, "package.json"))
	if err != nil {
		return fmt.Errorf("read package.json: %w", err)
	}
	var project pluginProjectPackage
	if err := json.Unmarshal(data, &project); err != nil {
		return fmt.Errorf("parse package.json: %w", err)
	}
	if strings.TrimSpace(project.Scripts["build"]) == "" {
		return errors.New("package.json must define a non-empty scripts.build command")
	}
	manager, err := resolvePluginPackageManager(abs, project.PackageManager, packageManagerOverride)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, manager, "run", "build")
	cmd.Dir = abs
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s build script: %w", manager, err)
	}
	return validateDeclaredBuildArtifacts(abs)
}

func validateDeclaredBuildArtifacts(root string) error {
	var manifest struct {
		Desktop *struct {
			Entry string `json:"entry"`
		} `json:"desktop"`
		Runtime *struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"runtime"`
	}
	data, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return fmt.Errorf("read plugin.json: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse plugin.json: %w", err)
	}
	paths := make([]string, 0, 2)
	if manifest.Desktop != nil {
		paths = append(paths, manifest.Desktop.Entry)
	}
	if manifest.Runtime != nil {
		if strings.ContainsAny(manifest.Runtime.Command, `/\\`) && !filepath.IsAbs(manifest.Runtime.Command) {
			paths = append(paths, manifest.Runtime.Command)
		} else {
			for _, arg := range manifest.Runtime.Args {
				ext := strings.ToLower(filepath.Ext(arg))
				if !strings.HasPrefix(arg, "-") && (ext == ".js" || ext == ".mjs" || ext == ".cjs") {
					paths = append(paths, arg)
					break
				}
			}
		}
	}
	if len(paths) == 0 {
		return errors.New("plugin.json must declare a desktop entry or executable runtime artifact")
	}
	for _, relative := range paths {
		clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relative)))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("declared build artifact must be package-relative: %q", relative)
		}
		info, err := os.Stat(filepath.Join(root, clean))
		if err != nil {
			return fmt.Errorf("declared build artifact %s is missing: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("declared build artifact %s is not a regular file", relative)
		}
	}
	return nil
}

func resolvePluginPackageManager(root, declared, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if name := strings.TrimSpace(strings.SplitN(declared, "@", 2)[0]); name != "" {
		switch name {
		case "npm", "pnpm", "yarn", "bun":
			return name, nil
		default:
			return "", fmt.Errorf("unsupported packageManager %q", declared)
		}
	}
	for _, candidate := range []struct{ lockfile, command string }{
		{"pnpm-lock.yaml", "pnpm"}, {"yarn.lock", "yarn"}, {"bun.lock", "bun"}, {"bun.lockb", "bun"},
	} {
		if _, err := os.Stat(filepath.Join(root, candidate.lockfile)); err == nil {
			return candidate.command, nil
		}
	}
	return "npm", nil
}

type pluginDiagnostic struct {
	Level   string `json:"level"`
	Check   string `json:"check"`
	Message string `json:"message"`
}

type pluginTestOutput struct {
	OK          bool               `json:"ok"`
	Diagnostics []pluginDiagnostic `json:"diagnostics"`
}

func runPluginTest(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin test")
	timeout := fs.Duration("timeout", 30*time.Second, "Runtime contract timeout")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() != 1 {
		return pluginCLIError(errors.New("plugin test requires a plugin directory path"))
	}
	diagnostics := testPluginPackage(fs.Arg(0), *timeout)
	failed := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Level == "fail" {
			failed = true
		}
	}
	if *jsonOutput {
		if err := printPluginJSON(pluginTestOutput{OK: !failed, Diagnostics: diagnostics}); err != nil {
			return err
		}
	} else {
		for _, diagnostic := range diagnostics {
			fmt.Printf("[%s] %s: %s\n", strings.ToUpper(diagnostic.Level), diagnostic.Check, diagnostic.Message)
		}
	}
	if failed {
		return pluginCLIError(errors.New("plugin contract test failed"))
	}
	return nil
}

func testPluginPackage(source string, timeout time.Duration) []pluginDiagnostic {
	diagnostics := make([]pluginDiagnostic, 0, 6)
	prepared, cleanup, err := preparePluginSource(source)
	if err != nil {
		return append(diagnostics, pluginDiagnostic{Level: "fail", Check: "package.prepare", Message: err.Error()})
	}
	defer cleanup()
	inspection, err := pluginpkg.InspectPackage(prepared)
	if err != nil {
		return append(diagnostics, pluginDiagnostic{Level: "fail", Check: "package.validate", Message: err.Error()})
	}
	diagnostics = append(diagnostics, pluginDiagnostic{Level: "pass", Check: "package.validate", Message: inspection.Fingerprint})
	plugin, err := pluginpkg.LoadManifest(filepath.Join(prepared, "plugin.json"), "contract-test")
	if err != nil {
		return append(diagnostics, pluginDiagnostic{Level: "fail", Check: "manifest.load", Message: err.Error()})
	}
	if plugin.Runtime == nil {
		return append(diagnostics, pluginDiagnostic{Level: "skip", Check: "runtime.initialize", Message: "plugin has no runtime entry"})
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := pluginhost.Start(ctx, pluginhost.ProcessConfig{
		ID: plugin.ID, Command: plugin.Runtime.Command, Args: plugin.Runtime.Args, Env: plugin.Runtime.Env,
		PluginRoot: plugin.Root, WuuHome: filepath.Join(os.TempDir(), "wuu-plugin-test"), Timeout: timeout,
	})
	if err != nil {
		return append(diagnostics, pluginDiagnostic{Level: "fail", Check: "runtime.initialize", Message: err.Error()})
	}
	defer client.Close(context.Background())
	diagnostics = append(diagnostics, pluginDiagnostic{Level: "pass", Check: "runtime.initialize", Message: "runtime completed executable initialization"})
	protocolVersion := client.ProtocolVersion()
	if protocolVersion < pluginhost.ProtocolVersion || protocolVersion > pluginhost.CapabilityProtocolVersion {
		return append(diagnostics, pluginDiagnostic{Level: "fail", Check: "runtime.protocol", Message: fmt.Sprintf("unsupported negotiated protocol version %d", protocolVersion)})
	}
	diagnostics = append(diagnostics, pluginDiagnostic{Level: "pass", Check: "runtime.protocol", Message: fmt.Sprintf("negotiated protocol v%d", protocolVersion)})

	capabilities := client.Capabilities()
	for _, descriptor := range capabilities {
		if err := pluginhost.ValidateCapabilityDescriptor(descriptor); err != nil {
			return append(diagnostics, pluginDiagnostic{Level: "fail", Check: "runtime.capabilities", Message: err.Error()})
		}
	}
	diagnostics = append(diagnostics, pluginDiagnostic{Level: "pass", Check: "runtime.capabilities", Message: fmt.Sprintf("validated %d capability descriptor(s)", len(capabilities))})

	tools := client.Tools()
	toolIDs := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolIDs = append(toolIDs, tool.ID)
	}
	message := fmt.Sprintf("validated %d tool registration(s)", len(tools))
	if len(toolIDs) > 0 {
		message += ": " + strings.Join(toolIDs, ", ")
	}
	return append(diagnostics, pluginDiagnostic{Level: "pass", Check: "runtime.tools", Message: message})
}

func runPluginPack(args []string) error {
	fs := flag.NewFlagSet("plugin pack", flag.ExitOnError)
	output := fs.String("output", "", "Output .zip path (defaults to <plugin-id>-<version>.zip)")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() == 0 {
		return pluginCLIError(errors.New("plugin pack requires a plugin directory path"))
	}
	source := fs.Arg(0)
	prepared, cleanup, err := preparePluginSource(source)
	if err != nil {
		return pluginCLIError(fmt.Errorf("pack: %w", err))
	}
	defer cleanup()
	inspection, err := pluginpkg.InspectPackage(prepared)
	if err != nil {
		return pluginCLIError(fmt.Errorf("pack: %w", err))
	}

	outPath := strings.TrimSpace(*output)
	if outPath == "" {
		outPath = fmt.Sprintf("%s-%s.zip", inspection.ID, inspection.Version)
	}

	// Create zip from the validated plugin directory.
	if err := pluginpkg.PackToZip(prepared, outPath); err != nil {
		return pluginCLIError(fmt.Errorf("pack: %w", err))
	}

	fmt.Printf("Packed %s v%s → %s\n", inspection.ID, inspection.Version, outPath)
	fmt.Printf("  fingerprint: %s\n", inspection.Fingerprint)
	return nil
}

// DevAuthorization records a one-time dev directory grant.
type DevAuthorization = pluginpkg.DevAuthorization

var errDevGenerationBusy = errors.New("plugin catalog is being read or published by another operation")

func runPluginDevMode(args []string) error {
	fs := flag.NewFlagSet("plugin dev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	watch := fs.Bool("watch", true, "Watch for file changes and auto-reload")
	pollInterval := fs.Duration("poll", 2*time.Second, "Source reconciliation interval (must be positive)")
	packageManager := fs.String("package-manager", "", "Package manager executable")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() != 1 {
		return pluginCLIError(errors.New("plugin dev requires one TypeScript file or plugin directory path"))
	}
	if *pollInterval <= 0 {
		return pluginCLIError(errors.New("poll interval must be positive"))
	}
	dir, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return pluginCLIError(fmt.Errorf("resolve source: %w", err))
	}

	wuuHome, err := statepath.Home("")
	if err != nil {
		return pluginCLIError(fmt.Errorf("dev: %w", err))
	}

	devDir := filepath.Join(wuuHome, "dev", "plugins")
	if err := os.MkdirAll(devDir, 0o700); err != nil {
		return pluginCLIError(fmt.Errorf("create dev dir: %w", err))
	}

	pluginID, err := readPluginIdentity(dir)
	if err != nil {
		return pluginCLIError(fmt.Errorf("dev: %w", err))
	}
	if err := authorizeDevDirectory(devDir, pluginID, dir); err != nil {
		return pluginCLIError(fmt.Errorf("dev authorization: %w", err))
	}

	fmt.Printf("Dev mode authorized for %s\n", pluginID)
	fmt.Printf("  source:     %s\n", dir)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	initialSnapshot := snapshotPluginSource(dir)
	initialRefreshPending := false
	if diagnostic, err := refreshDevGeneration(ctx, wuuHome, dir, strings.TrimSpace(*packageManager)); err != nil {
		diagnostic.Message = err.Error()
		printDevDiagnostic(diagnostic)
		if !*watch {
			return pluginCLIError(err)
		}
		initialRefreshPending = errors.Is(err, errDevGenerationBusy)
	} else {
		printDevDiagnostic(diagnostic)
	}
	if *watch {
		fmt.Printf("  watching:   yes (fsnotify; source reconciliation every %s)\n", pollInterval.String())
		fmt.Printf("  Save source files to refresh the isolated development generation.\n")
	}

	if *watch {
		watchDevDirFS(ctx, wuuHome, dir, strings.TrimSpace(*packageManager), *pollInterval, initialSnapshot, initialRefreshPending)
	}

	return nil
}

func readPluginIdentity(dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		if link, err := os.Lstat(dir); err != nil {
			return "", err
		} else if link.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("single-file development source must not be a symlink; use its target path")
		}
		if !info.Mode().IsRegular() || strings.ToLower(filepath.Ext(dir)) != ".ts" {
			return "", errors.New("development source must be a .ts file or plugin directory")
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return singleFilePluginID(abs), nil
	}
	var identity struct {
		ID string `json:"id"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return "", err
	}
	if strings.TrimSpace(identity.ID) == "" {
		return "", errors.New("plugin manifest id is required")
	}
	return identity.ID, nil
}

func authorizeDevDirectory(devDir, pluginID, directory string) error {
	info, err := os.Stat(directory)
	if err != nil {
		return err
	}
	sourceKind := "file"
	if info.IsDir() {
		sourceKind = "directory"
	}
	authPath := filepath.Join(devDir, pluginID+".json")
	if data, err := os.ReadFile(authPath); err == nil {
		var existing DevAuthorization
		if json.Unmarshal(data, &existing) == nil && existing.PluginID == pluginID && existing.Directory == directory {
			existingKind := existing.SourceKind
			if existingKind == "" {
				existingKind = "directory"
			}
			if existingKind != sourceKind {
				return fmt.Errorf("plugin %q is already authorized for a different source kind", pluginID)
			}
			return nil
		}
		return fmt.Errorf("plugin %q is already authorized for a different directory", pluginID)
	} else if !os.IsNotExist(err) {
		return err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	auth := DevAuthorization{PluginID: pluginID, Directory: directory, SourceKind: sourceKind, Token: hex.EncodeToString(tokenBytes), CreatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(devDir, ".authorization-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, authPath)
}

func refreshDevGeneration(ctx context.Context, wuuHome, dir, packageManager string) (pluginDiagnostic, error) {
	probe, acquired, err := session.TryAcquirePluginCatalogMutationLease(wuuHome)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation", Message: err.Error()}, fmt.Errorf("dev generation mutation check failed; previous generation preserved: %w", err)
	}
	if !acquired {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation", Message: errDevGenerationBusy.Error()}, fmt.Errorf("dev generation refresh deferred; previous generation preserved: %w", errDevGenerationBusy)
	}
	if err := probe.Release(); err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation", Message: err.Error()}, fmt.Errorf("release dev generation mutation check: %w", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.prepare", Message: err.Error()}, fmt.Errorf("dev source unavailable; previous generation preserved: %w", err)
	}
	sourceID, err := readPluginIdentity(dir)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.authorization", Message: err.Error()}, err
	}
	authorization, err := pluginpkg.ReadDevAuthorization(wuuHome, sourceID)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.authorization", Message: err.Error()}, fmt.Errorf("dev authorization failed; previous generation preserved: %w", err)
	}
	expectedKind := authorization.SourceKind
	if expectedKind == "" {
		expectedKind = "directory"
	}
	actualKind := "file"
	if info.IsDir() {
		actualKind = "directory"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.authorization", Message: err.Error()}, err
	}
	if authorization.Directory != abs || expectedKind != actualKind {
		err := errors.New("development source path or source kind differs from its authorization; previous generation preserved")
		return pluginDiagnostic{Level: "fail", Check: "dev.authorization", Message: err.Error()}, err
	}
	var prepared string
	var cleanup func()
	if info.IsDir() {
		if err := executePluginBuild(ctx, dir, packageManager); err != nil {
			return pluginDiagnostic{Level: "fail", Check: "dev.build", Message: err.Error()}, fmt.Errorf("dev build failed; previous generation preserved: %w", err)
		}
		prepared, cleanup, err = preparePluginSource(dir)
	} else {
		prepared, cleanup, err = prepareSingleFilePlugin(ctx, dir)
	}
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.prepare", Message: err.Error()}, fmt.Errorf("dev source preparation failed; previous generation preserved: %w", err)
	}
	defer cleanup()
	inspection, err := pluginpkg.InspectPackage(prepared)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.validate", Message: err.Error()}, fmt.Errorf("dev validation failed; previous generation preserved: %w", err)
	}
	manifest, err := pluginpkg.LoadManifest(filepath.Join(prepared, "plugin.json"), "dev")
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.validate", Message: err.Error()}, fmt.Errorf("dev validation failed; previous generation preserved: %w", err)
	}
	if err := pluginpkg.CheckMinimumWuuVersion(manifest.MinimumWuuVersion, version.Info().Version); err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.compat", Message: err.Error()}, fmt.Errorf("dev compatibility check failed; previous generation preserved: %w", err)
	}
	if inspection.ID != authorization.PluginID {
		err := errors.New("prepared plugin identity differs from the authorized source; previous generation preserved")
		return pluginDiagnostic{Level: "fail", Check: "dev.authorization", Message: err.Error()}, err
	}
	lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(wuuHome)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation", Message: err.Error()}, fmt.Errorf("dev generation mutation failed; previous generation preserved: %w", err)
	}
	if !acquired {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation", Message: errDevGenerationBusy.Error()}, fmt.Errorf("dev generation refresh deferred; previous generation preserved: %w", errDevGenerationBusy)
	}
	defer lease.Release()
	published, err := pluginpkg.PublishDevGeneration(wuuHome, dir, prepared, authorization)
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.refresh", Message: err.Error()}, fmt.Errorf("dev generation refresh failed; previous generation preserved: %w", err)
	}
	epoch, err := lease.Advance()
	if err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation"}, fmt.Errorf("dev package published but generation signal failed: %w", err)
	}
	if err := lease.Release(); err != nil {
		return pluginDiagnostic{Level: "fail", Check: "dev.mutation"}, fmt.Errorf("dev package published but catalog lease release failed: %w", err)
	}
	return pluginDiagnostic{Level: "pass", Check: "dev.refresh", Message: fmt.Sprintf("published generation %s at epoch %d from %s", published.Fingerprint, epoch, published.Root)}, nil
}

func preparePluginSource(source string) (string, func(), error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", func() {}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", func() {}, err
	}
	if !info.IsDir() {
		return abs, func() {}, nil
	}
	staging, err := os.MkdirTemp("", "wuu-plugin-package-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && relative != "." && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		destination := filepath.Join(staging, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin package contains unsupported file %s", relative)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		if copyErr != nil {
			output.Close()
			return copyErr
		}
		if inputErr != nil {
			output.Close()
			return inputErr
		}
		return output.Close()
	})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return staging, cleanup, nil
}

func printDevDiagnostic(diagnostic pluginDiagnostic) {
	data, _ := json.Marshal(diagnostic)
	fmt.Println(string(data))
}

// printDevReloadHint loads the current manifest and prints the one-line effect
// hint for the paths that changed during the last dev refresh.
func printDevReloadHint(dir string, changed []string) {
	manifest, err := pluginpkg.LoadManifest(filepath.Join(dir, pluginpkg.ManifestFilename), "dev")
	if err != nil {
		return
	}
	hint := pluginpkg.ClassifyReload(manifest.Manifest, changed)
	if hint.Message != "" {
		fmt.Printf("  effect: %s\n", hint.Message)
	}
}

func validatePluginName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("plugin name must not be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("plugin name %q is too long (max 64 chars)", name)
	}
	for i, c := range name {
		if i == 0 && (c < 'a' || c > 'z') {
			return fmt.Errorf("plugin name %q must start with a lowercase letter", name)
		}
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return fmt.Errorf("plugin name %q contains invalid character '%c'", name, c)
		}
	}
	return nil
}

func pluginScaffoldSources(pluginType string) map[string]string {
	runtime := `import { runJSONLRuntime, type RuntimePlugin } from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  initialize(_params) {
    return { hooks: [], tools: [] };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
`
	desktop := `import type { PluginGenerationApi } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  api.registerSlot("conversation.header", {
    id: "hello",
    render() {
      return api.react.createElement("span", null, "Hello from " + api.pluginId);
    },
  });
}
`
	switch pluginType {
	case "agent":
		return map[string]string{"index.ts": runtime}
	case "desktop":
		return map[string]string{"index.ts": desktop}
	default:
		return map[string]string{"runtime.ts": runtime, "renderer.ts": desktop}
	}
}

func pluginScaffoldPackageJSON(name string) string {
	data, _ := json.MarshalIndent(map[string]any{
		"name":        name,
		"version":     "0.1.0",
		"private":     true,
		"type":        "module",
		"description": fmt.Sprintf("A Wuu plugin: %s", name),
		"scripts": map[string]string{
			"build":     "tsc",
			"typecheck": "tsc --noEmit",
		},
		"devDependencies": map[string]string{
			"typescript":      "^5.9.0",
			"@types/node":     "^22.0.0",
			"@wuu/plugin-sdk": "^0.1.0",
		},
	}, "", "  ")
	return string(data) + "\n"
}

func pluginScaffoldTSConfig() string {
	data, _ := json.MarshalIndent(map[string]any{
		"compilerOptions": map[string]any{
			"target":           "ES2022",
			"module":           "ESNext",
			"moduleResolution": "bundler",
			"outDir":           "dist",
			"rootDir":          "src",
			"strict":           true,
			"declaration":      true,
			"skipLibCheck":     true,
		},
		"include": []string{"src"},
	}, "", "  ")
	return string(data) + "\n"
}
