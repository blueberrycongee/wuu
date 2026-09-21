package appserver

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestPluginPackageInspectZipWithoutRuntime(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "inspect.zip")
	writePluginPackageZip(t, archive, map[string]string{
		"inspect-demo/plugin.json": `{"id":"inspect-demo","name":"Inspect Demo","version":"1.2.3"}`,
		"inspect-demo/README.md":   "hello",
	})

	out := &lockedBuffer{}
	srv := &Server{out: out}
	callPluginPackageRPC(t, srv, "inspect", MethodPluginPackageInspect, PluginPackageInspectParams{Path: archive})
	response := responseByID(t, parseOutput(t, out.String()), "inspect")
	if response["error"] != nil {
		t.Fatalf("inspect response = %+v", response)
	}
	result := remarshal[PluginPackageInspectResult](t, response["result"])
	if result.Package.ID != "inspect-demo" || result.Package.Name != "Inspect Demo" || result.Package.Version != "1.2.3" {
		t.Fatalf("package metadata = %+v", result.Package)
	}
	if result.Package.SourceKind != PluginPackageSourceZip || result.Package.ArchiveRoot != "inspect-demo" || result.Package.ManifestPath != "inspect-demo/plugin.json" {
		t.Fatalf("package source metadata = %+v", result.Package)
	}
	if result.Package.FileCount != 2 || result.Package.Fingerprint == "" {
		t.Fatalf("package inspection stats = %+v", result.Package)
	}
}

func TestPluginDesktopModuleReadRequiresExactApprovedGeneration(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	root := t.TempDir()
	manifestPath := filepath.Join(root, "plugin.json")
	entryPath := filepath.Join(root, "dist", "desktop.js")
	writePluginPackageFile(t, manifestPath, `{
  "schemaVersion": 1,
  "id": "desktop-demo",
  "desktop": {"entry": "dist/desktop.js"}
}`)
	writePluginPackageFile(t, entryPath, `export function activate(api) { api.registerCleanup(() => {}); }`)
	item, err := pluginpkg.LoadManifest(manifestPath, "user")
	if err != nil {
		t.Fatalf("load plugin manifest: %v", err)
	}
	rt.Plugins = []pluginpkg.Plugin{item}
	if rt.ExtensionSettings == nil {
		rt.ExtensionSettings = &extensions.Settings{}
	}
	if err := rt.ExtensionSettings.RecordGrant(extensions.Grant{
		SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Permissions: item.EffectivePermissions,
	}); err != nil {
		t.Fatalf("grant plugin: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	callPluginPackageRPC(t, srv, "read", MethodPluginDesktopModuleRead, PluginDesktopModuleReadParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint,
	})
	response := responseByID(t, parseOutput(t, out.String()), "read")
	if response["error"] != nil {
		t.Fatalf("read response = %+v", response)
	}
	result := remarshal[PluginDesktopModuleReadResult](t, response["result"])
	if result.ID != item.SubjectID || result.Fingerprint != item.Fingerprint || result.Entry != "dist/desktop.js" {
		t.Fatalf("module identity = %+v", result)
	}
	if !strings.Contains(result.Source, "activate") || result.Digest == "" || result.MediaType != "text/javascript" {
		t.Fatalf("module payload = %+v", result)
	}

	writePluginPackageFile(t, entryPath, `export function activate() { throw new Error("changed"); }`)
	callPluginPackageRPC(t, srv, "changed", MethodPluginDesktopModuleRead, PluginDesktopModuleReadParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint,
	})
	changed := responseByID(t, parseOutput(t, out.String()), "changed")
	if changed["error"] == nil || !strings.Contains(fmt.Sprint(changed["error"]), "changed") {
		t.Fatalf("changed response = %+v", changed)
	}
}

func TestPluginDesktopModuleReadPreservesDevelopmentAuthorization(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	root := t.TempDir()
	manifestPath := filepath.Join(root, "plugin.json")
	writePluginPackageFile(t, manifestPath, `{"id":"dev-desktop","desktop":{"entry":"desktop.js"}}`)
	writePluginPackageFile(t, filepath.Join(root, "desktop.js"), `export const activate = () => {};`)
	item, err := pluginpkg.LoadManifest(manifestPath, "dev")
	if err != nil {
		t.Fatalf("load plugin manifest: %v", err)
	}
	item.AuthorizedDev = true
	rt.ExtensionSettings = &extensions.Settings{}
	rt.Plugins = []pluginpkg.Plugin{item}

	out := &lockedBuffer{}
	srv := New(rt, out)
	callPluginPackageRPC(t, srv, "dev", MethodPluginDesktopModuleRead, PluginDesktopModuleReadParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint,
	})
	response := responseByID(t, parseOutput(t, out.String()), "dev")
	if response["error"] != nil {
		t.Fatalf("development desktop module response = %+v", response)
	}
	result := remarshal[PluginDesktopModuleReadResult](t, response["result"])
	if result.ID != item.SubjectID || !strings.Contains(result.Source, "activate") {
		t.Fatalf("development desktop module = %+v", result)
	}
}

func TestPluginDesktopModuleReadRejectsDisabledPlugin(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	root := t.TempDir()
	manifestPath := filepath.Join(root, "plugin.json")
	writePluginPackageFile(t, manifestPath, `{"id":"disabled-desktop","desktop":{"entry":"desktop.js"}}`)
	writePluginPackageFile(t, filepath.Join(root, "desktop.js"), `export const activate = () => {};`)
	item, err := pluginpkg.LoadManifest(manifestPath, "user")
	if err != nil {
		t.Fatalf("load plugin manifest: %v", err)
	}
	settings := &extensions.Settings{}
	if err := settings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint}); err != nil {
		t.Fatalf("grant plugin: %v", err)
	}
	settings.SetDisabled(item.SubjectID, true)
	rt.ExtensionSettings = settings
	rt.Plugins = []pluginpkg.Plugin{item}

	out := &lockedBuffer{}
	srv := New(rt, out)
	callPluginPackageRPC(t, srv, "disabled", MethodPluginDesktopModuleRead, PluginDesktopModuleReadParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint,
	})
	response := responseByID(t, parseOutput(t, out.String()), "disabled")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "not approved and enabled") {
		t.Fatalf("disabled response = %+v", response)
	}
}

func TestPluginPackageInstallDirectoryIsPendingAndDoesNotActivate(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	source := t.TempDir()
	marker := filepath.Join(source, "executed")
	writePluginPackageFile(t, filepath.Join(source, "plugin.json"), `{
  "id": "pending-demo",
  "name": "Pending Demo",
  "version": "2.0.0",
  "skills": ["skills"],
  "runtime": {"protocol":"wuu-plugin-v1","command":"bin/plugin"}
}`)
	writePluginPackageFile(t, filepath.Join(source, "skills", "pending-skill", "SKILL.md"), "pending skill")
	writePluginPackageFile(t, filepath.Join(source, "bin", "plugin"), "#!/bin/sh\ntouch "+marker+"\n")

	out := &lockedBuffer{}
	srv := New(rt, out)
	callPluginPackageRPC(t, srv, "install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
	response := responseByID(t, parseOutput(t, out.String()), "install")
	if response["error"] != nil {
		t.Fatalf("install response = %+v", response)
	}
	result := remarshal[PluginPackageInstallResult](t, response["result"])
	if result.Package.ID != "pending-demo" || result.Package.SourceKind != PluginPackageSourceDirectory || result.Replaced {
		t.Fatalf("install result = %+v", result)
	}
	record := pluginPackageRecord(t, result.ExtensionInventory, "pending-demo")
	if record.ApprovalState != ExtensionApprovalPending || record.State != ExtensionStatePending || record.RuntimeState != ExtensionRuntimeInactive {
		t.Fatalf("pending inventory record = %+v", record)
	}
	state := capturePluginRuntimeState(srv)
	if activePluginVersion(state.active, "pending-demo") != "" {
		t.Fatalf("active plugins = %+v, want no unapproved plugin", state.active)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("plugin runtime executed during install: %v", err)
	}
	installedManifest := filepath.Join(rt.WuuHome, "plugins", "pending-demo", "plugin.json")
	if _, err := os.Stat(installedManifest); err != nil {
		t.Fatalf("installed manifest under Wuu home: %v", err)
	}
	for _, skill := range result.Skills {
		if skill.Name == "pending-skill" {
			t.Fatalf("unapproved plugin skill was activated: %+v", skill)
		}
	}
}

func TestPluginPackageUpdateWaitsForExactApprovalBeforePromotion(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	configPath, err := statepath.ConfigPath(rt.HomeDir)
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	writePluginPackageFile(t, configPath, `{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "model": "fake-model"}}
}`)

	versionOne := writeManagedPluginPackage(t, "update-demo", "1.0.0", "first generation")
	out := &lockedBuffer{}
	srv := New(rt, out)
	callPluginPackageRPC(t, srv, "install-v1", MethodPluginPackageInstall, PluginPackageInstallParams{Path: versionOne})
	installResponse := responseByID(t, parseOutput(t, out.String()), "install-v1")
	installed := remarshal[PluginPackageInstallResult](t, installResponse["result"])
	installedRecord := pluginPackageRecord(t, installed.ExtensionInventory, "update-demo")
	callPluginPackageRPC(t, srv, "grant-v1", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: installedRecord.ID, Fingerprint: installedRecord.Fingerprint, Action: ExtensionPackageGrant,
	})
	if response := responseByID(t, parseOutput(t, out.String()), "grant-v1"); response["error"] != nil {
		t.Fatalf("grant v1 response = %+v", response)
	}

	versionTwo := writeManagedPluginPackage(t, "update-demo", "2.0.0", "second generation")
	callPluginPackageRPC(t, srv, "stage-v2", MethodPluginPackageInstall, PluginPackageInstallParams{Path: versionTwo})
	stageResponse := responseByID(t, parseOutput(t, out.String()), "stage-v2")
	if stageResponse["error"] != nil {
		t.Fatalf("stage v2 response = %+v", stageResponse)
	}
	staged := remarshal[PluginPackageInstallResult](t, stageResponse["result"])
	if !staged.Pending || staged.Replaced || staged.ActiveFingerprint != installed.Package.Fingerprint {
		t.Fatalf("staged result = %+v", staged)
	}
	stagedRecord := pluginPackageRecord(t, staged.ExtensionInventory, "update-demo")
	if stagedRecord.PendingUpdate == nil || stagedRecord.PendingUpdate.Version != "2.0.0" || stagedRecord.PendingUpdate.Fingerprint != staged.Package.Fingerprint {
		t.Fatalf("staged inventory record = %+v", stagedRecord)
	}
	state := capturePluginRuntimeState(srv)
	if activePluginVersion(state.plugins, "update-demo") != "1.0.0" || activePluginVersion(state.active, "update-demo") != "1.0.0" {
		t.Fatalf("active generation changed before approval: plugins=%+v active=%+v", state.plugins, state.active)
	}

	callPluginPackageRPC(t, srv, "stale-promote", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: stagedRecord.ID, Fingerprint: installed.Package.Fingerprint, Action: ExtensionPackagePromoteUpdate,
	})
	if response := responseByID(t, parseOutput(t, out.String()), "stale-promote"); response["error"] == nil {
		t.Fatalf("stale promotion response = %+v", response)
	}
	state = capturePluginRuntimeState(srv)
	if activePluginVersion(state.active, "update-demo") != "1.0.0" {
		t.Fatalf("stale approval changed active generation: %+v", state.active)
	}

	callPluginPackageRPC(t, srv, "promote-v2", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: stagedRecord.ID, Fingerprint: staged.Package.Fingerprint, Action: ExtensionPackagePromoteUpdate,
	})
	promoteResponse := responseByID(t, parseOutput(t, out.String()), "promote-v2")
	if promoteResponse["error"] != nil {
		t.Fatalf("promote v2 response = %+v", promoteResponse)
	}
	promoted := remarshal[ExtensionPackageUpdateResult](t, promoteResponse["result"])
	promotedRecord := pluginPackageRecord(t, promoted.ExtensionInventory, "update-demo")
	if promotedRecord.PendingUpdate != nil || promotedRecord.Fingerprint != staged.Package.Fingerprint {
		t.Fatalf("promoted inventory record = %+v", promotedRecord)
	}
	state = capturePluginRuntimeState(srv)
	if activePluginVersion(state.active, "update-demo") != "2.0.0" {
		t.Fatalf("approved generation was not activated: %+v", state.active)
	}
	if state.settings == nil {
		t.Fatal("approved update settings were not applied")
	}
	grant, ok := state.settings.FindGrant(stagedRecord.ID, staged.Package.Fingerprint)
	if !ok || grant.Fingerprint != staged.Package.Fingerprint {
		t.Fatalf("approved update grant = %+v, %v", grant, ok)
	}

	versionThree := writeManagedPluginPackage(t, "update-demo", "3.0.0", "third generation")
	callPluginPackageRPC(t, srv, "stage-v3", MethodPluginPackageInstall, PluginPackageInstallParams{Path: versionThree})
	stageThreeResponse := responseByID(t, parseOutput(t, out.String()), "stage-v3")
	stageThree := remarshal[PluginPackageInstallResult](t, stageThreeResponse["result"])
	callPluginPackageRPC(t, srv, "reject-v3", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: stagedRecord.ID, Fingerprint: stageThree.Package.Fingerprint, Action: ExtensionPackageRejectUpdate,
	})
	rejectResponse := responseByID(t, parseOutput(t, out.String()), "reject-v3")
	if rejectResponse["error"] != nil {
		t.Fatalf("reject v3 response = %+v", rejectResponse)
	}
	state = capturePluginRuntimeState(srv)
	if activePluginVersion(state.active, "update-demo") != "2.0.0" {
		t.Fatalf("rejection changed active generation: %+v", state.active)
	}
	if _, err := pluginpkg.ReadPendingUpdate(rt.WuuHome, "update-demo"); !errors.Is(err, pluginpkg.ErrPendingUpdateNotFound) {
		t.Fatalf("pending update after rejection = %v", err)
	}
}

func TestPluginPackageUpdateActivationFailureKeepsInstalledAndPendingGenerations(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	configPath, err := statepath.ConfigPath(rt.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, configPath, `{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "model": "fake-model"}}
}`)

	versionOne := writeManagedPluginPackage(t, "activation-failure", "1.0.0", "working generation")
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	callPluginPackageRPC(t, srv, "install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: versionOne})
	installed := remarshal[PluginPackageInstallResult](t, responseByID(t, parseOutput(t, out.String()), "install")["result"])
	installedRecord := pluginPackageRecord(t, installed.ExtensionInventory, "activation-failure")
	callPluginPackageRPC(t, srv, "grant", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: installedRecord.ID, Fingerprint: installedRecord.Fingerprint, Action: ExtensionPackageGrant,
	})
	if response := responseByID(t, parseOutput(t, out.String()), "grant"); response["error"] != nil {
		t.Fatalf("grant response = %+v", response)
	}

	versionTwo := t.TempDir()
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, filepath.Join(versionTwo, "plugin.json"), fmt.Sprintf(`{
  "id": "activation-failure",
  "version": "2.0.0",
  "runtime": {"protocol":"wuu-plugin-v1","command":%q,"args":["-test.run=^$"]}
}`, testExecutable))
	callPluginPackageRPC(t, srv, "stage", MethodPluginPackageInstall, PluginPackageInstallParams{Path: versionTwo})
	staged := remarshal[PluginPackageInstallResult](t, responseByID(t, parseOutput(t, out.String()), "stage")["result"])
	callPluginPackageRPC(t, srv, "promote", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: installedRecord.ID, Fingerprint: staged.Package.Fingerprint, Action: ExtensionPackagePromoteUpdate,
	})
	if message := responseErrorMessage(t, responseByID(t, parseOutput(t, out.String()), "promote")); !strings.Contains(message, "activate pending plugin update") {
		t.Fatalf("activation error = %q", message)
	}
	// Runtime fields are generation-owned and may be rewritten by the watcher.
	// Stop the server before inspecting the final rollback snapshot directly.
	srv.Close()

	active, err := pluginpkg.InspectPackage(filepath.Join(rt.WuuHome, "plugins", "activation-failure"))
	if err != nil {
		t.Fatal(err)
	}
	if active.Version != "1.0.0" || active.Fingerprint != installed.Package.Fingerprint {
		t.Fatalf("installed generation changed: %+v", active)
	}
	pending, err := pluginpkg.ReadPendingUpdate(rt.WuuHome, "activation-failure")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Package.Version != "2.0.0" || pending.Package.Fingerprint != staged.Package.Fingerprint {
		t.Fatalf("pending generation changed: %+v", pending.Package)
	}
	state := capturePluginRuntimeState(srv)
	if activePluginVersion(state.active, "activation-failure") != "1.0.0" {
		t.Fatalf("live generation changed: %+v", state.active)
	}
	if state.settings == nil {
		t.Fatal("rollback settings were not applied")
	}
	grant, ok := state.settings.Grants[installedRecord.ID]
	if !ok || grant.Fingerprint != installed.Package.Fingerprint {
		t.Fatalf("approval was not rolled back: %+v", state.settings.Grants)
	}
	cfg, _, err := config.LoadPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	durableGrant, ok := cfg.Extensions.Grants[installedRecord.ID]
	if !ok || durableGrant.Fingerprint != installed.Package.Fingerprint {
		t.Fatalf("durable approval was not rolled back: %+v", cfg.Extensions.Grants)
	}
}

func TestPluginGenerationMutationExcludesActiveAndNewThreadWork(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	active := &threadState{ID: "active", admissionReserved: true}
	srv := &Server{rt: rt, threads: map[string]*threadState{"active": active}}

	if _, err := srv.beginPluginGenerationMutation("change", pluginGenerationMutationExclusive); err == nil {
		t.Fatal("mutation unexpectedly admitted while a thread reservation was active")
	}
	if srv.pluginGenerationMutation.Load() {
		t.Fatal("failed mutation left admission closed")
	}

	active.admissionReserved = false
	release, err := srv.beginPluginGenerationMutation("change", pluginGenerationMutationExclusive)
	if err != nil {
		t.Fatal(err)
	}
	newThread := &threadState{ID: "new"}
	newThread.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(newThread)
	newThread.mu.Unlock()
	if err != nil {
		release()
		t.Fatal(err)
	}
	if acquired {
		release()
		t.Fatal("new turn admitted during plugin generation mutation")
	}
	release()

	newThread.mu.Lock()
	acquired, err = srv.tryAcquireThreadExecutionLeaseLocked(newThread)
	if acquired {
		newThread.releaseThreadExecutionLeaseLocked()
	}
	newThread.mu.Unlock()
	if err != nil || !acquired {
		t.Fatalf("turn admission did not reopen: acquired=%v err=%v", acquired, err)
	}
}

func TestLivePluginPolicyMutationIsAllowedWhileTurnRuns(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	active := &threadState{ID: "active", admissionReserved: true, running: true}
	srv := &Server{rt: rt, threads: map[string]*threadState{"active": active}}
	release, err := srv.beginPluginGenerationMutation("change", pluginGenerationMutationLive)
	if err != nil {
		t.Fatalf("live policy change blocked by a running turn: %v", err)
	}
	release()
	if _, err := srv.beginPluginGenerationMutation("remove", pluginGenerationMutationExclusive); err == nil {
		t.Fatal("exclusive removal unexpectedly admitted while a turn was running")
	}
}

func TestPluginGenerationMutationIsBlockedByAnotherAppServerExecution(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	executingServer := &Server{rt: rt, threads: map[string]*threadState{}}
	mutatingServer := &Server{rt: rt, threads: map[string]*threadState{}}
	thread := &threadState{ID: "other-server-turn"}

	thread.mu.Lock()
	acquired, err := executingServer.tryAcquireThreadExecutionLeaseLocked(thread)
	thread.mu.Unlock()
	if err != nil || !acquired {
		t.Fatalf("acquire execution generation: acquired=%v err=%v", acquired, err)
	}
	thread.mu.Lock()
	thread.running = true
	thread.releaseThreadExecutionLeaseLocked()
	thread.mu.Unlock()
	if _, err := mutatingServer.beginPluginGenerationMutation("change", pluginGenerationMutationExclusive); err == nil {
		t.Fatal("mutation unexpectedly crossed another app-server's active turn generation")
	}

	thread.mu.Lock()
	thread.running = false
	thread.maybeReleasePluginGenerationExecutionLeaseLocked()
	thread.mu.Unlock()
	release, err := mutatingServer.beginPluginGenerationMutation("change", pluginGenerationMutationExclusive)
	if err != nil {
		t.Fatalf("mutation remained blocked after execution release: %v", err)
	}
	release()
}

func TestConcurrentPluginGenerationAdmissionsRefreshOnce(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	mutation, acquired, err := session.TryAcquirePluginGenerationMutationLease(rt.WuuHome)
	if err != nil || !acquired {
		t.Fatalf("acquire mutation generation: acquired=%v err=%v", acquired, err)
	}
	if _, err := mutation.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := mutation.Release(); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{}, 2)
	allowRefresh := make(chan struct{})
	var calls atomic.Int32
	srv := &Server{
		rt:      rt,
		threads: map[string]*threadState{},
		refreshExtensionsForTest: func(config.Config) error {
			calls.Add(1)
			started <- struct{}{}
			<-allowRefresh
			return nil
		},
	}
	type admissionResult struct {
		acquired bool
		err      error
	}
	results := make(chan admissionResult, 2)
	admit := func(id string) {
		thread := &threadState{ID: id}
		thread.mu.Lock()
		ok, err := srv.tryAcquireThreadExecutionLeaseLocked(thread)
		if ok {
			thread.releaseThreadExecutionLeaseLocked()
		}
		thread.mu.Unlock()
		results <- admissionResult{acquired: ok, err: err}
	}
	go admit("first")
	go admit("second")
	<-started
	time.Sleep(50 * time.Millisecond)
	close(allowRefresh)
	for range 2 {
		result := <-results
		if result.err != nil || !result.acquired {
			t.Fatalf("admission failed: acquired=%v err=%v", result.acquired, result.err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("generation refresh calls = %d, want 1", got)
	}
}

func TestServerStartupRefreshesGenerationAdvancedAfterRuntimeConstruction(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	oldHost := rt.PluginHost
	mutation, acquired, err := session.TryAcquirePluginGenerationMutationLease(rt.WuuHome)
	if err != nil || !acquired {
		t.Fatalf("acquire mutation generation: acquired=%v err=%v", acquired, err)
	}
	epoch, err := mutation.Advance()
	if err != nil {
		t.Fatal(err)
	}
	if err := mutation.Release(); err != nil {
		t.Fatal(err)
	}

	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	if srv.startupErr != nil {
		t.Fatalf("server startup: %v", srv.startupErr)
	}
	if got := srv.pluginGenerationEpoch.Load(); got != epoch {
		t.Fatalf("server generation epoch = %d, want %d", got, epoch)
	}
	if rt.PluginHost == oldHost {
		t.Fatal("server accepted the stale runtime plugin generation")
	}
}

func TestPluginPackageRemoveRefreshesInventory(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	configPath, err := statepath.ConfigPath(rt.HomeDir)
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}
	writePluginPackageFile(t, configPath, `{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "model": "fake-model"}}
}`)
	source := t.TempDir()
	writePluginPackageFile(t, filepath.Join(source, "plugin.json"), `{"id":"remove-demo","skills":["skills"]}`)
	writePluginPackageFile(t, filepath.Join(source, "skills", "remove-skill", "SKILL.md"), `---
name: remove-skill
description: Verifies plugin package removal.
---

# Remove skill
`)

	out := &lockedBuffer{}
	srv := New(rt, out)
	callPluginPackageRPC(t, srv, "install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
	installResponse := responseByID(t, parseOutput(t, out.String()), "install")
	if installResponse["error"] != nil {
		t.Fatalf("install response = %+v", installResponse)
	}
	installed := remarshal[PluginPackageInstallResult](t, installResponse["result"])
	installedRecord := pluginPackageRecord(t, installed.ExtensionInventory, "remove-demo")
	callPluginPackageRPC(t, srv, "grant", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{
		ID: installedRecord.ID, Fingerprint: installedRecord.Fingerprint, Action: ExtensionPackageGrant,
	})
	grantResponse := responseByID(t, parseOutput(t, out.String()), "grant")
	if grantResponse["error"] != nil {
		t.Fatalf("grant response = %+v", grantResponse)
	}
	state := capturePluginRuntimeState(srv)
	foundPlugin := false
	for _, active := range state.active {
		foundPlugin = foundPlugin || active.ID == "remove-demo"
	}
	if !foundPlugin {
		t.Fatalf("active plugins before removal = %+v", state.active)
	}
	foundSkill := false
	for _, name := range state.skillNames {
		foundSkill = foundSkill || name == "remove-skill"
	}
	if !foundSkill {
		t.Fatalf("approved plugin skill was not activated: %+v", state.skillNames)
	}

	callPluginPackageRPC(t, srv, "remove", MethodPluginPackageRemove, PluginPackageRemoveParams{ID: "remove-demo"})
	response := responseByID(t, parseOutput(t, out.String()), "remove")
	if response["error"] != nil {
		t.Fatalf("remove response = %+v", response)
	}
	result := remarshal[PluginPackageRemoveResult](t, response["result"])
	if result.ID != "remove-demo" || !result.Removed {
		t.Fatalf("remove result = %+v", result)
	}
	for _, record := range result.ExtensionInventory {
		if record.Provenance.PluginID == "remove-demo" {
			t.Fatalf("removed plugin remains in inventory: %+v", record)
		}
	}
	if _, err := os.Stat(filepath.Join(rt.WuuHome, "plugins", "remove-demo")); !os.IsNotExist(err) {
		t.Fatalf("removed plugin still exists: %v", err)
	}
	state = capturePluginRuntimeState(srv)
	if activePluginVersion(state.active, "remove-demo") != "" {
		t.Fatalf("removed plugin remains active: %+v", state.active)
	}
	for _, name := range state.skillNames {
		if name == "remove-skill" {
			t.Fatalf("removed plugin skill remains active: %q", name)
		}
	}
	if state.settings != nil {
		if _, ok := state.settings.Grants[extensions.SubjectID("user", "remove-demo")]; ok {
			t.Fatalf("removed plugin grant remains persisted: %+v", state.settings.Grants)
		}
	}
}

func TestPluginPackageHandlersRejectInvalidPathAndID(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	out := &lockedBuffer{}
	srv := New(rt, out)

	callPluginPackageRPC(t, srv, "bad-path", MethodPluginPackageInstall, PluginPackageInstallParams{Path: filepath.Join(t.TempDir(), "missing")})
	callPluginPackageRPC(t, srv, "bad-id", MethodPluginPackageRemove, PluginPackageRemoveParams{ID: "../outside"})
	messages := parseOutput(t, out.String())
	pathError := responseErrorMessage(t, responseByID(t, messages, "bad-path"))
	if !strings.Contains(pathError, "install plugin package") || !strings.Contains(pathError, "missing") {
		t.Fatalf("invalid path error = %q", pathError)
	}
	idError := responseErrorMessage(t, responseByID(t, messages, "bad-id"))
	if !strings.Contains(idError, "remove plugin package") || !strings.Contains(idError, "portable path component") {
		t.Fatalf("invalid id error = %q", idError)
	}
	if _, err := os.Stat(filepath.Join(rt.WuuHome, "plugins")); !os.IsNotExist(err) {
		t.Fatalf("invalid requests mutated plugin directory: %v", err)
	}
}

func TestPluginPackageActivationMutationsRejectRunningTurn(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	source := t.TempDir()
	writePluginPackageFile(t, filepath.Join(source, "plugin.json"), `{"id":"busy-demo"}`)
	out := &lockedBuffer{}
	srv := New(rt, out)
	thread := &threadState{ID: "busy", running: true}
	srv.mu.Lock()
	srv.threads[thread.ID] = thread
	srv.mu.Unlock()

	// Installation is catalog-only and must proceed while a turn runs.
	callPluginPackageRPC(t, srv, "install-busy", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
	// Activation-class mutations swap the live generation and must wait.
	callPluginPackageRPC(t, srv, "remove-busy", MethodPluginPackageRemove, PluginPackageRemoveParams{ID: "busy-demo"})
	messages := parseOutput(t, out.String())
	install := responseByID(t, messages, "install-busy")
	if _, hasError := install["error"]; hasError {
		t.Fatalf("install error = %v", install["error"])
	}
	message := responseErrorMessage(t, responseByID(t, messages, "remove-busy"))
	if !strings.Contains(message, "while a turn is running") {
		t.Fatalf("remove error = %q", message)
	}
	if _, err := os.Stat(filepath.Join(rt.WuuHome, "plugins", "busy-demo")); err != nil {
		t.Fatalf("running-turn install did not write the package: %v", err)
	}
}

func callPluginPackageRPC(t *testing.T, srv *Server, id, method string, params any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("call %s: %v", method, err)
	}
}

func pluginPackageRecord(t *testing.T, records []ExtensionInventoryRecord, pluginID string) ExtensionInventoryRecord {
	t.Helper()
	for _, record := range records {
		if record.Provenance.PluginID == pluginID && record.Kind == "plugin" {
			return record
		}
	}
	t.Fatalf("plugin %q not found in inventory: %+v", pluginID, records)
	return ExtensionInventoryRecord{}
}

func activePluginVersion(plugins []pluginpkg.Plugin, id string) string {
	for _, plugin := range plugins {
		if plugin.ID == id {
			return plugin.Version
		}
	}
	return ""
}

type pluginRuntimeState struct {
	plugins    []pluginpkg.Plugin
	active     []pluginpkg.Plugin
	skillNames []string
	settings   *extensions.Settings
}

func capturePluginRuntimeState(srv *Server) pluginRuntimeState {
	srv.pluginGenerationRefreshMu.Lock()
	defer srv.pluginGenerationRefreshMu.Unlock()

	state := pluginRuntimeState{
		plugins: append([]pluginpkg.Plugin(nil), srv.rt.Plugins...),
		active:  append([]pluginpkg.Plugin(nil), srv.rt.ActivePlugins...),
	}
	for _, skill := range srv.rt.Skills {
		state.skillNames = append(state.skillNames, skill.Name)
	}
	if srv.rt.ExtensionSettings == nil {
		return state
	}
	settings := &extensions.Settings{
		Grants:   make(map[string]extensions.Grant, len(srv.rt.ExtensionSettings.Grants)),
		Disabled: make(map[string]bool, len(srv.rt.ExtensionSettings.Disabled)),
		Rejected: make(map[string]extensions.PolicyDecision, len(srv.rt.ExtensionSettings.Rejected)),
	}
	for id, grant := range srv.rt.ExtensionSettings.Grants {
		grant.Permissions = append([]string(nil), grant.Permissions...)
		settings.Grants[id] = grant
	}
	for id, disabled := range srv.rt.ExtensionSettings.Disabled {
		settings.Disabled[id] = disabled
	}
	for id, rejection := range srv.rt.ExtensionSettings.Rejected {
		settings.Rejected[id] = rejection
	}
	state.settings = settings
	return state
}

func responseErrorMessage(t *testing.T, response map[string]any) string {
	t.Helper()
	if response["error"] == nil {
		t.Fatalf("response unexpectedly succeeded: %+v", response)
	}
	return remarshal[ResponseError](t, response["error"]).Message
}

func writePluginPackageFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeManagedPluginPackage(t *testing.T, id, version, contents string) string {
	t.Helper()
	source := t.TempDir()
	writePluginPackageFile(t, filepath.Join(source, "plugin.json"), fmt.Sprintf(`{"id":%q,"version":%q,"skills":["skills"]}`, id, version))
	writePluginPackageFile(t, filepath.Join(source, "skills", "managed-skill", "SKILL.md"), fmt.Sprintf(`---
name: managed-skill
description: Verifies managed plugin generations.
---

# Managed skill

%s
`, contents))
	return source
}

func writePluginPackageZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, contents := range entries {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
