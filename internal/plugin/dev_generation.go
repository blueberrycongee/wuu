package plugin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	devAuthorizationDir      = "plugins"
	devGenerationDir         = "generations"
	devGenerationPkg         = "package"
	devGenerationReceiptFile = "receipt.json"
)

// DevAuthorization is the durable, one-time grant for one plugin development
// source. An empty SourceKind denotes a legacy directory grant. Token is never
// copied into a published generation.
type DevAuthorization struct {
	PluginID   string    `json:"plugin_id"`
	Directory  string    `json:"directory"`
	SourceKind string    `json:"source_kind,omitempty"`
	Token      string    `json:"token,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type devGenerationReceipt struct {
	PluginID    string    `json:"plugin_id"`
	Directory   string    `json:"directory"`
	Fingerprint string    `json:"fingerprint"`
	Signature   string    `json:"signature"`
	PublishedAt time.Time `json:"published_at"`
}

// DevAuthorizationPath returns the only authorization record consulted for a
// local development plugin.
func DevAuthorizationPath(wuuHome, pluginID string) (string, error) {
	if err := validateInstallID(pluginID); err != nil {
		return "", err
	}
	if strings.TrimSpace(wuuHome) == "" {
		return "", errors.New("Wuu home is required")
	}
	return filepath.Join(wuuHome, "dev", devAuthorizationDir, pluginID+".json"), nil
}

func ReadDevAuthorization(wuuHome, pluginID string) (DevAuthorization, error) {
	path, err := DevAuthorizationPath(wuuHome, pluginID)
	if err != nil {
		return DevAuthorization{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DevAuthorization{}, fmt.Errorf("read dev authorization: %w", err)
	}
	var authorization DevAuthorization
	if err := json.Unmarshal(data, &authorization); err != nil {
		return DevAuthorization{}, fmt.Errorf("parse dev authorization: %w", err)
	}
	if authorization.PluginID != pluginID || strings.TrimSpace(authorization.Directory) == "" || strings.TrimSpace(authorization.Token) == "" {
		return DevAuthorization{}, errors.New("dev authorization is incomplete or belongs to another plugin")
	}
	return authorization, nil
}

// PublishDevGeneration stages a complete package and its authorization receipt
// before replacing the host-consumed development generation. Discovery can
// recover the previous signed package if the process exits between renames.
// Callers must hold the exclusive plugin-generation mutation lease.
func PublishDevGeneration(wuuHome, developerDirectory, source string, authorization DevAuthorization) (Plugin, error) {
	if strings.TrimSpace(authorization.Token) == "" {
		return Plugin{}, errors.New("dev authorization token is required")
	}
	developerDirectory, err := filepath.Abs(developerDirectory)
	if err != nil {
		return Plugin{}, fmt.Errorf("resolve developer directory: %w", err)
	}
	authorizedDirectory, err := filepath.Abs(authorization.Directory)
	if err != nil {
		return Plugin{}, fmt.Errorf("resolve authorized directory: %w", err)
	}
	if developerDirectory != authorizedDirectory {
		return Plugin{}, errors.New("developer directory does not match its one-time authorization")
	}
	if err := validateInstallID(authorization.PluginID); err != nil {
		return Plugin{}, err
	}

	root := filepath.Join(wuuHome, "dev", devGenerationDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Plugin{}, fmt.Errorf("create dev generation root: %w", err)
	}
	container, err := os.MkdirTemp(root, ".publish-")
	if err != nil {
		return Plugin{}, fmt.Errorf("create dev generation staging directory: %w", err)
	}
	cleanupContainer := true
	defer func() {
		if cleanupContainer {
			_ = os.RemoveAll(container)
		}
	}()

	stagingHome := filepath.Join(container, "staging")
	installed, err := InstallPackage(stagingHome, source)
	if err != nil {
		return Plugin{}, fmt.Errorf("stage dev generation: %w", err)
	}
	if installed.Plugin.ID != authorization.PluginID {
		return Plugin{}, fmt.Errorf("authorized plugin %q cannot publish package %q", authorization.PluginID, installed.Plugin.ID)
	}
	manifestRelative, err := filepath.Rel(installed.Destination, installed.Plugin.ManifestPath)
	if err != nil {
		return Plugin{}, fmt.Errorf("resolve staged dev manifest: %w", err)
	}
	packageRoot := filepath.Join(container, devGenerationPkg)
	if err := os.Rename(installed.Destination, packageRoot); err != nil {
		return Plugin{}, fmt.Errorf("stage dev package container: %w", err)
	}
	_ = os.RemoveAll(stagingHome)
	stagedDev, err := LoadManifest(filepath.Join(packageRoot, manifestRelative), "dev")
	if err != nil {
		return Plugin{}, fmt.Errorf("validate staged dev provenance: %w", err)
	}
	receipt := devGenerationReceipt{
		PluginID:    authorization.PluginID,
		Directory:   authorizedDirectory,
		Fingerprint: stagedDev.Fingerprint,
		PublishedAt: time.Now().UTC(),
	}
	receipt.Signature = signDevGenerationReceipt(receipt, authorization.Token)
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return Plugin{}, fmt.Errorf("marshal dev generation receipt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(container, devGenerationReceiptFile), append(data, '\n'), 0o600); err != nil {
		return Plugin{}, fmt.Errorf("write dev generation receipt: %w", err)
	}

	destination := filepath.Join(root, authorization.PluginID)
	backup, err := reserveDevGenerationBackup(root)
	if err != nil {
		return Plugin{}, err
	}
	cleanupBackup := true
	defer func() {
		if cleanupBackup {
			_ = os.RemoveAll(backup)
		}
	}()
	hadPrevious := false
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Rename(destination, backup); err != nil {
			return Plugin{}, fmt.Errorf("stage previous dev generation: %w", err)
		}
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		return Plugin{}, fmt.Errorf("inspect previous dev generation: %w", err)
	}
	if err := os.Rename(container, destination); err != nil {
		if hadPrevious {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				cleanupBackup = false
				return Plugin{}, fmt.Errorf("publish dev generation: %w (restore failed: %v; previous generation remains at %s)", err, restoreErr, backup)
			}
		}
		return Plugin{}, fmt.Errorf("publish dev generation: %w", err)
	}
	cleanupContainer = false
	published, err := loadAuthorizedDevGeneration(wuuHome, destination)
	if err != nil {
		failed := destination + ".failed"
		moveErr := os.Rename(destination, failed)
		var restoreErr error
		if hadPrevious {
			if moveErr == nil {
				restoreErr = os.Rename(backup, destination)
			}
		}
		if moveErr != nil || restoreErr != nil {
			cleanupBackup = false
			return Plugin{}, fmt.Errorf("validate published dev generation: %w (rollback failed: move=%v restore=%v; recovery files remain at %s and %s)", err, moveErr, restoreErr, backup, failed)
		}
		_ = os.RemoveAll(failed)
		return Plugin{}, fmt.Errorf("validate published dev generation: %w", err)
	}
	if published.ManifestPath != filepath.Join(destination, devGenerationPkg, manifestRelative) {
		return Plugin{}, errors.New("published dev manifest path changed unexpectedly")
	}
	return published, nil
}

func discoverAuthorizedDev(wuuHome string) []Plugin {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return nil
	}
	root := filepath.Join(wuuHome, "dev", devGenerationDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]Plugin, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item, err := loadAuthorizedDevGeneration(wuuHome, filepath.Join(root, entry.Name()))
		if err == nil {
			out = append(out, item)
		}
	}
	// Publication uses two renames. A process exit between them leaves the last
	// good generation in a hidden backup. Read it in place: discovery can run
	// under a shared execution lease, so it must not rename or delete files.
	// A present destination always wins, including an invalid one; recovery
	// must not hide a failed integrity check or replace another directory.
	recovered := make(map[string]Plugin)
	ambiguous := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".previous-") {
			continue
		}
		item, err := loadAuthorizedDevGeneration(wuuHome, filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, item.ID)); !os.IsNotExist(err) {
			continue
		}
		if _, exists := recovered[item.ID]; exists {
			ambiguous[item.ID] = true
		}
		recovered[item.ID] = item
	}
	for id, item := range recovered {
		// Multiple interrupted publications cannot be ordered by the unsigned
		// receipt timestamp. Keep the files for explicit recovery instead.
		if !ambiguous[id] {
			out = append(out, item)
		}
	}
	return out
}

func loadAuthorizedDevGeneration(wuuHome, container string) (Plugin, error) {
	data, err := os.ReadFile(filepath.Join(container, devGenerationReceiptFile))
	if err != nil {
		return Plugin{}, err
	}
	var receipt devGenerationReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return Plugin{}, err
	}
	if filepath.Base(container) != receipt.PluginID && !strings.HasPrefix(filepath.Base(container), ".previous-") {
		return Plugin{}, errors.New("dev generation container does not match its receipt")
	}
	authorization, err := ReadDevAuthorization(wuuHome, receipt.PluginID)
	if err != nil {
		return Plugin{}, err
	}
	authorizedDirectory, err := filepath.Abs(authorization.Directory)
	if err != nil || receipt.Directory != authorizedDirectory {
		return Plugin{}, errors.New("dev generation directory authorization does not match")
	}
	expected, err := hex.DecodeString(signDevGenerationReceipt(receipt, authorization.Token))
	if err != nil {
		return Plugin{}, err
	}
	actual, err := hex.DecodeString(receipt.Signature)
	if err != nil || !hmac.Equal(actual, expected) {
		return Plugin{}, errors.New("dev generation authorization receipt is invalid")
	}
	item, err := loadPluginDir(filepath.Join(container, devGenerationPkg), "dev", "")
	if err != nil {
		return Plugin{}, err
	}
	if item.ID != receipt.PluginID || item.Fingerprint != receipt.Fingerprint {
		return Plugin{}, errors.New("dev generation package does not match its authorization receipt")
	}
	item.AuthorizedDev = true
	return item, nil
}

func signDevGenerationReceipt(receipt devGenerationReceipt, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s", receipt.PluginID, receipt.Directory, receipt.Fingerprint)
	return hex.EncodeToString(mac.Sum(nil))
}

func reserveDevGenerationBackup(root string) (string, error) {
	path, err := os.MkdirTemp(root, ".previous-")
	if err != nil {
		return "", fmt.Errorf("reserve dev generation backup: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("prepare dev generation backup: %w", err)
	}
	return path, nil
}
