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
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
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

func runPluginInstall(args []string) error { return runPluginPackageInstall("install", args) }
func runPluginUpdate(args []string) error  { return runPluginPackageInstall("update", args) }

func runPluginPackageInstall(action string, args []string) (retErr error) {
	fs, jsonOutput := pluginFlagSet("plugin " + action)
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	source := ""
	if action == "update" {
		if fs.NArg() != 2 {
			return pluginCLIError(errors.New("plugin update requires an installed plugin id and one local directory or .zip path"))
		}
		source = fs.Arg(1)
	} else {
		var err error
		source, err = requiredPluginArgument(fs, "plugin install requires one local directory or .zip path")
		if err != nil {
			return err
		}
	}
	inspection, err := pluginpkg.InspectPackage(source)
	if err != nil {
		return pluginCLIError(err)
	}
	if action == "update" && inspection.ID != fs.Arg(0) {
		return pluginCLIError(fmt.Errorf("plugin update id %q does not match package id %q", fs.Arg(0), inspection.ID))
	}
	identity, err := pluginpkg.PackageSourceIdentity(source)
	if err != nil {
		return pluginCLIError(err)
	}
	home, err := statepath.Home("")
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	binding, err := openCommandState(ctx, "")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, binding.Close()) }()
	services, err := binding.Services()
	if err != nil {
		return err
	}
	release, err := beginPluginCLIMutation(home, action)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, release()) }()
	var pending *pluginpkg.PendingUpdate
	var fresh *pluginpkg.InstallResult
	preservePackage := false
	defer func() {
		if fresh != nil && !preservePackage {
			retErr = errors.Join(retErr, pluginpkg.RollbackInstallation(home, fresh.Plugin.ID, fresh.Plugin.Fingerprint))
		}
	}()
	if _, err := os.Lstat(filepath.Join(home, "plugins", inspection.ID)); err == nil {
		update, err := pluginpkg.StagePackageUpdate(home, identity)
		if err != nil {
			return pluginCLIError(err)
		}
		pending = &update
	} else if !os.IsNotExist(err) {
		return err
	} else if action == "update" {
		return pluginCLIError(fmt.Errorf("plugin %q is not installed", inspection.ID))
	}
	subject := extensions.SubjectID("user", inspection.ID)
	if pending != nil {
		view, err := services.Config.Read(ctx)
		if err != nil {
			return err
		}
		trusted := view.Config.Extensions != nil && view.Config.Extensions.TrustsSource(subject, pending.ActiveFingerprint, identity)
		clear(view.Source)
		if !trusted {
			output := packageInspectionOutput(pending.Package)
			output.Destination = pending.Path
			output.Pending = true
			output.ActiveFingerprint = pending.ActiveFingerprint
			output.ApprovalRequired = true
			return printPluginPackageOutput(output, *jsonOutput, "Staged plugin source change; confirmation is required")
		}
		inspection = pending.Package
	} else {
		installed, err := pluginpkg.InstallPackage(home, identity)
		if err != nil {
			return pluginCLIError(err)
		}
		inspection = installed.Package
		fresh = &installed
	}
	view, next, err := preparePluginPolicy(ctx, services.Config, func(settings *extensions.Settings) error {
		if pending != nil && !settings.TrustsSource(subject, pending.ActiveFingerprint, identity) {
			return errors.New("plugin source authorization changed; reread before retry")
		}
		return settings.TrustInstallation(subject, inspection.Fingerprint, identity, inspection.EffectivePermissions, pending == nil)
	})
	if err != nil {
		return err
	}
	defer clear(view.Source)
	defer clear(next)
	preservePackage = true // An unknown CAS may already have committed the matching grant.
	committed, policyErr := commitPluginPolicy(ctx, services.Config, view.Revision, next)
	var serviceErr *pluginhost.HostServiceError
	if errors.As(policyErr, &serviceErr) && serviceErr.Code == "config_conflict" {
		preservePackage = false
	}
	if !committed {
		return policyErr
	}
	if pending != nil {
		installed, err := pluginpkg.PromotePendingUpdate(home, inspection.ID, inspection.Fingerprint)
		if err != nil {
			return errors.Join(policyErr, fmt.Errorf("plugin policy committed, but package promotion failed: %w", err))
		}
		inspection = installed.Package
	}
	if policyErr != nil {
		return policyErr
	}
	output := packageInspectionOutput(inspection)
	output.Destination = filepath.Join(home, "plugins", inspection.ID)
	output.Replaced = pending != nil
	return printPluginPackageOutput(output, *jsonOutput, "Installed and trusted plugin package")
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

func runPluginRemove(args []string) (retErr error) {
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
	ctx, cancel := signalContext()
	defer cancel()
	// Opening the selected graph reads the catalog; do it before its mutation lease.
	binding, err := openCommandState(ctx, "")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, binding.Close()) }()
	services, err := binding.Services()
	if err != nil {
		return err
	}
	releaseMutation, err := beginPluginCLIMutation(home, "remove")
	if err != nil {
		return pluginCLIError(err)
	}
	defer func() { retErr = errors.Join(retErr, releaseMutation()) }()
	pending, pendingErr := pluginpkg.ReadPendingUpdate(home, id)
	if pendingErr != nil && !errors.Is(pendingErr, pluginpkg.ErrPendingUpdateNotFound) {
		return pluginCLIError(pendingErr)
	}
	view, source, err := preparePluginPolicy(ctx, services.Config, func(settings *extensions.Settings) error {
		settings.Revoke(extensions.SubjectID("user", id))
		return nil
	})
	if err != nil {
		return err
	}
	defer clear(view.Source)
	defer clear(source)
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
		// An absent selected source has no stored grant to revoke. In particular,
		// removing a freshly installed package must not create first-run config.
		var policyErr error
		if view.Source != nil {
			committed, writeErr := commitPluginPolicy(ctx, services.Config, view.Revision, source)
			if !committed {
				// Only package files are restored. An uncertain CAS may already have
				// revoked the grant; never issue a compensating configuration write.
				return pluginCLIError(rollback(fmt.Errorf("clear plugin policy; restoring package files only: %w", writeErr)))
			}
			policyErr = writeErr
		}
		var cleanupErr error
		if pendingRemoval != nil {
			cleanupErr = errors.Join(cleanupErr, pendingRemoval.Commit())
		}
		if packageRemoval != nil {
			cleanupErr = errors.Join(cleanupErr, packageRemoval.Commit())
		}
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("plugin package removed, but recovery-copy cleanup failed: %w", cleanupErr)
		}
		if err := errors.Join(policyErr, cleanupErr); err != nil {
			return err
		}
	}
	output := pluginPackageOutput{ID: result.ID, Destination: result.Destination, Removed: result.Removed}
	message := "Plugin package was not installed"
	if result.Removed {
		message = "Removed plugin package"
	}
	return printPluginPackageOutput(output, *jsonOutput, message)
}

func runPluginPolicy(action string, args []string) (retErr error) {
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
	ctx, cancel := signalContext()
	defer cancel()
	// Opening the selected graph reads the catalog; do it before its mutation lease.
	binding, err := openCommandState(ctx, *workdir)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, binding.Close()) }()
	services, err := binding.Services()
	if err != nil {
		return err
	}
	releaseMutation, err := beginPluginCLIMutation(home, action)
	if err != nil {
		return pluginCLIError(err)
	}
	defer func() { retErr = errors.Join(retErr, releaseMutation()) }()
	item, err := discoverPluginForPolicy(id, *workdir)
	if err != nil {
		return pluginCLIError(err)
	}
	if item.Official && (action == "approve" || action == "reject") {
		return pluginCLIError(fmt.Errorf("official bundled plugin %q cannot be approved or rejected", item.ID))
	}
	var approvedUpdate *pluginpkg.PendingUpdate
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
			approvedUpdate = &pending
			item.Fingerprint = pending.Package.Fingerprint
			item.EffectivePermissions = append([]string(nil), pending.Package.EffectivePermissions...)
		case pendingErr != nil && !errors.Is(pendingErr, pluginpkg.ErrPendingUpdateNotFound):
			return pluginCLIError(pendingErr)
		}
	}
	view, source, err := preparePluginPolicy(ctx, services.Config, func(settings *extensions.Settings) error {
		switch action {
		case "approve":
			scope := extensions.GrantScopeUser
			if item.Source == "project" {
				scope = extensions.GrantScopeProject
			}
			sourceIdentity := ""
			if approvedUpdate != nil {
				sourceIdentity = approvedUpdate.SourceIdentity
			}
			if err := settings.RecordGrant(extensions.Grant{
				SourceIdentity: sourceIdentity,
				SubjectID:      item.SubjectID,
				Fingerprint:    item.Fingerprint,
				Scope:          scope,
				Permissions:    append([]string(nil), item.EffectivePermissions...),
				ApprovedAt:     time.Now().UTC(),
			}); err != nil {
				return err
			}
			settings.SetEnabled(item.SubjectID, true)
			return nil
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
		return err
	}
	defer clear(view.Source)
	defer clear(source)
	committed, policyErr := commitPluginPolicy(ctx, services.Config, view.Revision, source)
	if !committed {
		return policyErr
	}
	if approvedUpdate != nil {
		// Publish package bytes only after the exact fingerprint grant is known
		// durable. Unknown CAS outcomes leave the old and pending packages intact.
		promoted, err := pluginpkg.PromotePendingUpdate(home, item.ID, approvedUpdate.Package.Fingerprint)
		if err != nil {
			return errors.Join(policyErr, fmt.Errorf("plugin policy committed, but pending update promotion failed: %w", err))
		}
		item = promoted.Plugin
	}
	if policyErr != nil {
		return policyErr
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

func preparePluginPolicy(ctx context.Context, client *config.Client, update func(*extensions.Settings) error) (config.SourceView, []byte, error) {
	view, err := client.Read(ctx)
	if err != nil {
		return config.SourceView{}, nil, fmt.Errorf("read selected plugin policy: %w", err)
	}
	source := view.Source
	if len(source) == 0 {
		// Empty source still has the selected provider's validated defaults.
		source, err = json.Marshal(view.Config)
		if err != nil {
			return config.SourceView{}, nil, fmt.Errorf("prepare initial selected plugin policy: %w", err)
		}
		defer clear(source)
	}
	next, _, err := config.UpdateExtensionSettingsSource(source, update)
	if err != nil {
		clear(view.Source)
		return config.SourceView{}, nil, fmt.Errorf("prepare selected plugin policy: %w", err)
	}
	return view, next, nil
}

func commitPluginPolicy(ctx context.Context, client *config.Client, revision string, source []byte) (bool, error) {
	result, err := client.CompareExchange(ctx, revision, source)
	defer clear(result.Source)
	if err != nil {
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) && serviceErr.Code == "config_conflict" {
			return false, fmt.Errorf("persist selected plugin policy: configuration changed; reread before retry: %w", err)
		}
		return false, fmt.Errorf("persist selected plugin policy: write outcome may be unknown; reconcile before retry: %w", err)
	}
	if !result.Committed {
		return false, errors.New("selected plugin policy write outcome is unknown: provider did not confirm commit; reconcile before retry")
	}
	if result.ActivationError != "" {
		return true, fmt.Errorf("plugin policy committed, but activation failed: %s", result.ActivationError)
	}
	return true, nil
}

func beginPluginCLIMutation(wuuHome, action string) (func() error, error) {
	lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(wuuHome)
	if err != nil {
		return nil, fmt.Errorf("begin plugin %s mutation: %w", action, err)
	}
	if !acquired {
		return nil, fmt.Errorf("cannot %s plugin packages while another app-server is changing the plugin catalog", action)
	}
	return func() error {
		_, advanceErr := lease.Advance()
		releaseErr := lease.Release()
		if err := errors.Join(advanceErr, releaseErr); err != nil {
			return fmt.Errorf("plugin %s catalog change may be saved, but publication could not be confirmed: %w", action, err)
		}
		return nil
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
