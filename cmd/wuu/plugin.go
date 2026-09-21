package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

type pluginPackageOutput struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name,omitempty"`
	Version              string   `json:"version,omitempty"`
	Description          string   `json:"description,omitempty"`
	SourcePath           string   `json:"source_path,omitempty"`
	SourceKind           string   `json:"source_kind,omitempty"`
	Destination          string   `json:"destination,omitempty"`
	ManifestPath         string   `json:"manifest_path,omitempty"`
	FileCount            int      `json:"file_count,omitempty"`
	UnpackedSize         int64    `json:"unpacked_size,omitempty"`
	Fingerprint          string   `json:"fingerprint,omitempty"`
	RequestedPermissions []string `json:"requested_permissions,omitempty"`
	EffectivePermissions []string `json:"effective_permissions,omitempty"`
	UnsupportedFields    []string `json:"unsupported_fields,omitempty"`
	Source               string   `json:"source,omitempty"`
	Root                 string   `json:"root,omitempty"`
	SubjectID            string   `json:"subject_id,omitempty"`
	Replaced             bool     `json:"replaced,omitempty"`
	ApprovalRequired     bool     `json:"approval_required,omitempty"`
	Removed              bool     `json:"removed,omitempty"`
	PolicyAction         string   `json:"policy_action,omitempty"`
	Pending              bool     `json:"pending,omitempty"`
	ActiveFingerprint    string   `json:"active_fingerprint,omitempty"`
}

func runPlugin(args []string) error {
	if len(args) == 0 {
		return pluginCLIError(errors.New("plugin subcommand is required (available: inspect, install, update, list, approve, reject, enable, disable, remove, create, validate, build, test, pack, dev)"))
	}
	switch args[0] {
	case "inspect":
		return runPluginInspect(args[1:])
	case "install":
		return runPluginInstall(args[1:])
	case "update":
		return runPluginUpdate(args[1:])
	case "list":
		return runPluginList(args[1:])
	case "approve", "reject", "enable", "disable":
		return runPluginPolicy(args[0], args[1:])
	case "remove", "uninstall":
		return runPluginRemove(args[1:])
	case "create", "validate", "build", "test", "pack", "dev":
		return runPluginDev(args)
	default:
		return pluginCLIError(fmt.Errorf("unknown plugin subcommand %q (available: inspect, install, update, list, approve, reject, enable, disable, remove, create, validate, build, test, pack, dev)", args[0]))
	}
}

func runPluginInspect(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin inspect")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	source, err := requiredPluginArgument(fs, "plugin inspect requires one local directory or .zip path")
	if err != nil {
		return err
	}
	inspection, err := pluginpkg.InspectPackage(source)
	if err != nil {
		return pluginCLIError(err)
	}
	return printPluginPackageOutput(packageInspectionOutput(inspection), *jsonOutput, "Valid plugin package")
}

func runPluginInstall(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin install")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	source, err := requiredPluginArgument(fs, "plugin install requires one local directory or .zip path")
	if err != nil {
		return err
	}
	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve Wuu home: %w", err)
	}
	inspection, err := pluginpkg.InspectPackage(source)
	if err != nil {
		return pluginCLIError(err)
	}
	releaseMutation, err := beginPluginCLIMutation(home, "install")
	if err != nil {
		return pluginCLIError(err)
	}
	defer releaseMutation()
	if _, statErr := os.Lstat(filepath.Join(home, "plugins", inspection.ID)); statErr == nil {
		pending, err := pluginpkg.StagePackageUpdate(home, source)
		if err != nil {
			return pluginCLIError(err)
		}
		output := packageInspectionOutput(pending.Package)
		output.Destination = pending.Path
		output.Pending = true
		output.ActiveFingerprint = pending.ActiveFingerprint
		output.ApprovalRequired = true
		return printPluginPackageOutput(output, *jsonOutput, "Staged plugin update; the installed generation remains active until approval")
	} else if !os.IsNotExist(statErr) {
		return pluginCLIError(fmt.Errorf("inspect installed plugin %q: %w", inspection.ID, statErr))
	}
	result, err := pluginpkg.InstallPackage(home, source)
	if err != nil {
		return pluginCLIError(err)
	}
	output := packageInspectionOutput(result.Package)
	output.Destination = result.Destination
	output.Replaced = result.Replaced
	output.ApprovalRequired = true
	return printPluginPackageOutput(output, *jsonOutput, "Installed plugin package; approval is required before code activation")
}

func runPluginUpdate(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin update")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		return pluginCLIError(errors.New("plugin update requires an installed plugin id and one local directory or .zip path"))
	}
	id, source := strings.TrimSpace(fs.Arg(0)), strings.TrimSpace(fs.Arg(1))
	inspection, err := pluginpkg.InspectPackage(source)
	if err != nil {
		return pluginCLIError(err)
	}
	if inspection.ID != id {
		return pluginCLIError(fmt.Errorf("plugin update id %q does not match package id %q", id, inspection.ID))
	}
	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve Wuu home: %w", err)
	}
	releaseMutation, err := beginPluginCLIMutation(home, "update")
	if err != nil {
		return pluginCLIError(err)
	}
	defer releaseMutation()
	pending, err := pluginpkg.StagePackageUpdate(home, source)
	if err != nil {
		return pluginCLIError(err)
	}
	output := packageInspectionOutput(pending.Package)
	output.Destination = pending.Path
	output.Pending = true
	output.ActiveFingerprint = pending.ActiveFingerprint
	output.ApprovalRequired = true
	return printPluginPackageOutput(output, *jsonOutput, "Staged plugin update; approve it to replace the installed generation")
}

func runPluginList(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin list")
	workdir := fs.String("workdir", "", "workspace directory whose project plugins should be included")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() != 0 {
		return pluginCLIError(errors.New("plugin list does not accept positional arguments"))
	}
	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve Wuu home: %w", err)
	}
	root := strings.TrimSpace(*workdir)
	if root != "" {
		root, err = filepath.Abs(root)
		if err != nil {
			return pluginCLIError(fmt.Errorf("resolve workspace: %w", err))
		}
	}
	plugins := pluginpkg.Discover(root, home)
	pendingByID := map[string]pluginpkg.PendingUpdate{}
	if pending, err := pluginpkg.ListPendingUpdates(home); err != nil {
		return pluginCLIError(err)
	} else {
		for _, update := range pending {
			pendingByID[update.Package.ID] = update
		}
	}
	output := make([]pluginPackageOutput, 0, len(plugins))
	for _, item := range plugins {
		record := pluginPackageOutput{
			ID:                   item.ID,
			Name:                 item.Name,
			Version:              item.Version,
			Description:          item.Description,
			Source:               item.Source,
			Root:                 item.Root,
			ManifestPath:         item.ManifestPath,
			Fingerprint:          item.Fingerprint,
			SubjectID:            item.SubjectID,
			RequestedPermissions: append([]string(nil), item.RequestedPermissions...),
			EffectivePermissions: append([]string(nil), item.EffectivePermissions...),
			UnsupportedFields:    append([]string(nil), item.UnsupportedFields...),
		}
		if pending, ok := pendingByID[item.ID]; ok && item.Source == "user" {
			record.Pending = true
			record.ActiveFingerprint = pending.ActiveFingerprint
		}
		output = append(output, record)
	}
	if *jsonOutput {
		return printPluginJSON(output)
	}
	if len(output) == 0 {
		fmt.Println("No plugins found.")
		return nil
	}
	sort.Slice(output, func(i, j int) bool { return output[i].ID < output[j].ID })
	for _, item := range output {
		version := item.Version
		if version != "" {
			version = " " + version
		}
		pending := ""
		if item.Pending {
			pending = "\tpending update"
		}
		fmt.Printf("%s%s\t%s\t%s%s\n", item.ID, version, item.Source, item.Root, pending)
	}
	return nil
}

func runPluginRemove(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin remove")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	id, err := requiredPluginArgument(fs, "plugin remove requires one installed plugin id")
	if err != nil {
		return err
	}
	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve Wuu home: %w", err)
	}
	releaseMutation, err := beginPluginCLIMutation(home, "remove")
	if err != nil {
		return pluginCLIError(err)
	}
	defer releaseMutation()
	pending, pendingErr := pluginpkg.ReadPendingUpdate(home, id)
	if pendingErr != nil && !errors.Is(pendingErr, pluginpkg.ErrPendingUpdateNotFound) {
		return pluginCLIError(pendingErr)
	}
	configPath, err := statepath.ConfigPath("")
	if err != nil {
		return fmt.Errorf("resolve user config: %w", err)
	}
	_, configStatErr := os.Stat(configPath)
	if configStatErr != nil && !os.IsNotExist(configStatErr) {
		return fmt.Errorf("inspect plugin policy: %w", configStatErr)
	}
	packageRemoval, result, err := pluginpkg.PrepareUninstallPackage(home, id)
	if err != nil {
		return pluginCLIError(err)
	}
	var pendingRemoval *pluginpkg.PendingUpdateRemoval
	rollback := func(cause error) error {
		var rollbackErr error
		if pendingRemoval != nil {
			rollbackErr = errors.Join(rollbackErr, pendingRemoval.Rollback())
		}
		if packageRemoval != nil {
			rollbackErr = errors.Join(rollbackErr, packageRemoval.Rollback())
		}
		if rollbackErr != nil {
			return fmt.Errorf("%w (rollback plugin removal: %v)", cause, rollbackErr)
		}
		return cause
	}
	if result.Removed {
		if pendingErr == nil {
			pendingRemoval, err = pluginpkg.PreparePendingUpdateRemoval(home, id, pending.Package.Fingerprint)
			if err != nil {
				return pluginCLIError(rollback(fmt.Errorf("clear pending plugin update: %w", err)))
			}
		}
		if configStatErr == nil {
			if _, err := config.UpdateExtensionSettings(configPath, func(settings *extensions.Settings) error {
				settings.Revoke(extensions.SubjectID("user", id))
				return nil
			}); err != nil {
				return pluginCLIError(rollback(fmt.Errorf("clear plugin policy: %w", err)))
			}
		}
		if pendingRemoval != nil {
			_ = pendingRemoval.Commit()
		}
		if packageRemoval != nil {
			_ = packageRemoval.Commit()
		}
	}
	output := pluginPackageOutput{ID: result.ID, Destination: result.Destination, Removed: result.Removed}
	message := "Plugin package was not installed"
	if result.Removed {
		message = "Removed plugin package"
	}
	return printPluginPackageOutput(output, *jsonOutput, message)
}

func runPluginPolicy(action string, args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin " + action)
	workdir := fs.String("workdir", "", "workspace directory for a project plugin")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	id, err := requiredPluginArgument(fs, "plugin "+action+" requires one discovered plugin id")
	if err != nil {
		return err
	}
	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve Wuu home: %w", err)
	}
	releaseMutation, err := beginPluginCLIMutation(home, action)
	if err != nil {
		return pluginCLIError(err)
	}
	defer releaseMutation()
	item, err := discoverPluginForPolicy(id, *workdir)
	if err != nil {
		return pluginCLIError(err)
	}
	if item.Official {
		return pluginCLIError(fmt.Errorf("official bundled plugin %q does not use user policy actions", item.ID))
	}
	if (action == "approve" || action == "reject") && item.Source == "user" {
		pending, pendingErr := pluginpkg.ReadPendingUpdate(home, item.ID)
		switch {
		case pendingErr == nil && action == "reject":
			if err := pluginpkg.RejectPendingUpdate(home, item.ID, pending.Package.Fingerprint); err != nil {
				return pluginCLIError(err)
			}
			output := packageInspectionOutput(pending.Package)
			output.Pending = true
			output.ActiveFingerprint = pending.ActiveFingerprint
			output.PolicyAction = "reject_update"
			return printPluginPackageOutput(output, *jsonOutput, "Rejected pending plugin update; the installed generation remains active")
		case pendingErr == nil && action == "approve":
			if _, err := pluginpkg.PromotePendingUpdate(home, item.ID, pending.Package.Fingerprint); err != nil {
				return pluginCLIError(err)
			}
			item, err = discoverPluginForPolicy(id, *workdir)
			if err != nil {
				return pluginCLIError(fmt.Errorf("plugin update was promoted but could not be rediscovered: %w", err))
			}
		case pendingErr != nil && !errors.Is(pendingErr, pluginpkg.ErrPendingUpdateNotFound):
			return pluginCLIError(pendingErr)
		}
	}
	configPath, err := statepath.ConfigPath("")
	if err != nil {
		return fmt.Errorf("resolve user config: %w", err)
	}
	_, err = config.UpdateExtensionSettings(configPath, func(settings *extensions.Settings) error {
		switch action {
		case "approve":
			scope := extensions.GrantScopeUser
			if item.Source == "project" {
				scope = extensions.GrantScopeProject
			}
			return settings.RecordGrant(extensions.Grant{
				SubjectID:   item.SubjectID,
				Fingerprint: item.Fingerprint,
				Scope:       scope,
				Permissions: append([]string(nil), item.EffectivePermissions...),
				ApprovedAt:  time.Now().UTC(),
			})
		case "reject":
			return settings.RecordRejection(item.SubjectID, item.Fingerprint)
		case "enable":
			settings.SetEnabled(item.SubjectID, true)
			return nil
		case "disable":
			settings.SetEnabled(item.SubjectID, false)
			return nil
		default:
			return fmt.Errorf("unsupported plugin policy action %q", action)
		}
	})
	if err != nil {
		return fmt.Errorf("persist plugin policy in %s: %w", configPath, err)
	}
	output := pluginPackageOutput{
		ID:                   item.ID,
		Name:                 item.Name,
		Version:              item.Version,
		Fingerprint:          item.Fingerprint,
		SubjectID:            item.SubjectID,
		Source:               item.Source,
		EffectivePermissions: append([]string(nil), item.EffectivePermissions...),
		PolicyAction:         action,
	}
	return printPluginPackageOutput(output, *jsonOutput, "Updated plugin policy")
}

func beginPluginCLIMutation(wuuHome, action string) (func(), error) {
	if action == "remove" {
		lease, acquired, err := session.TryAcquirePluginGenerationMutationLease(wuuHome)
		if err != nil {
			return nil, fmt.Errorf("begin plugin %s mutation: %w", action, err)
		}
		if !acquired {
			return nil, fmt.Errorf("plugin %s refused because executions currently own the active generation", action)
		}
		if _, err := lease.Advance(); err != nil {
			_ = lease.Release()
			return nil, fmt.Errorf("advance plugin %s generation: %w", action, err)
		}
		return func() { _ = lease.Release() }, nil
	}
	lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(wuuHome)
	if err != nil {
		return nil, fmt.Errorf("begin plugin %s mutation: %w", action, err)
	}
	if !acquired {
		return nil, fmt.Errorf("cannot %s plugin packages while another app-server is changing the plugin catalog", action)
	}
	return func() {
		if _, err := lease.Advance(); err != nil {
			_ = lease.Release()
			return
		}
		_ = lease.Release()
	}, nil
}

func discoverPluginForPolicy(id, workdir string) (pluginpkg.Plugin, error) {
	home, err := statepath.Home("")
	if err != nil {
		return pluginpkg.Plugin{}, fmt.Errorf("resolve Wuu home: %w", err)
	}
	root := strings.TrimSpace(workdir)
	if root != "" {
		root, err = filepath.Abs(root)
		if err != nil {
			return pluginpkg.Plugin{}, fmt.Errorf("resolve workspace: %w", err)
		}
	}
	for _, item := range pluginpkg.Discover(root, home) {
		if item.ID == id {
			return item, nil
		}
	}
	return pluginpkg.Plugin{}, fmt.Errorf("plugin %q was not found", id)
}

func pluginFlagSet(name string) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, fs.Bool("json", false, "output JSON")
}

func requiredPluginArgument(fs *flag.FlagSet, message string) (string, error) {
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return "", pluginCLIError(errors.New(message))
	}
	return fs.Arg(0), nil
}

func packageInspectionOutput(value pluginpkg.PackageInspection) pluginPackageOutput {
	return pluginPackageOutput{
		ID:                   value.ID,
		Name:                 value.Name,
		Version:              value.Version,
		Description:          value.Description,
		SourcePath:           value.SourcePath,
		SourceKind:           string(value.SourceKind),
		ManifestPath:         value.ManifestPath,
		FileCount:            value.FileCount,
		UnpackedSize:         value.UnpackedSize,
		Fingerprint:          value.Fingerprint,
		RequestedPermissions: append([]string(nil), value.RequestedPermissions...),
		EffectivePermissions: append([]string(nil), value.EffectivePermissions...),
		UnsupportedFields:    append([]string(nil), value.UnsupportedFields...),
	}
}

func printPluginPackageOutput(output pluginPackageOutput, asJSON bool, message string) error {
	if asJSON {
		return printPluginJSON(output)
	}
	fmt.Printf("%s: %s\n", message, output.ID)
	if output.Version != "" {
		fmt.Printf("Version: %s\n", output.Version)
	}
	if output.Destination != "" {
		fmt.Printf("Location: %s\n", output.Destination)
	} else if output.SourcePath != "" {
		fmt.Printf("Source: %s\n", output.SourcePath)
	}
	if output.Fingerprint != "" {
		fmt.Printf("Fingerprint: %s\n", output.Fingerprint)
	}
	return nil
}

func printPluginJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plugin output: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func pluginCLIError(err error) error {
	return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
}
