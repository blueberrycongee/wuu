package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const pendingUpdateMetadataFilename = "update.json"

var (
	// ErrPendingUpdateNotFound means no staged generation exists for the plugin.
	ErrPendingUpdateNotFound = errors.New("pending plugin update not found")
	// ErrPendingUpdateFingerprintMismatch means the caller did not name the
	// exact generation currently awaiting a decision.
	ErrPendingUpdateFingerprintMismatch = errors.New("pending plugin update fingerprint does not match")
)

// PendingUpdate describes one validated generation waiting outside the user
// plugin discovery roots. Package.SourcePath points to the private staged copy,
// not to the caller's original package.
type PendingUpdate struct {
	SourceIdentity    string            `json:"source_identity,omitempty"`
	Package           PackageInspection `json:"package"`
	ActiveFingerprint string            `json:"active_fingerprint"`
	Path              string            `json:"path"`
}

// PendingUpdateRemoval keeps a rejected update outside discovery until the
// caller commits or rolls back the surrounding package transaction.
type PendingUpdateRemoval struct {
	destination string
	tombstone   string
	finished    bool
}

type pendingUpdateMetadata struct {
	SourceIdentity    string            `json:"source_identity,omitempty"`
	Package           PackageInspection `json:"package"`
	ActiveFingerprint string            `json:"active_fingerprint"`
}

var pendingUpdatesMu sync.Mutex

// StagePackageUpdate validates and copies a directory or zip into the pending
// update store. The package must update an installed user plugin with the same
// id and different content. Staging never publishes or executes the package.
func StagePackageUpdate(wuuHome, source string) (PendingUpdate, error) {
	identity, err := PackageSourceIdentity(source)
	if err != nil {
		return PendingUpdate{}, err
	}
	return StagePackageUpdateFromSource(wuuHome, identity, identity)
}

// StagePackageUpdateFromSource stages resolved bytes while retaining the
// distribution identity used by the trust policy. Callers resolve/download
// before acquiring the catalog lease; staging itself never uses the network.
func StagePackageUpdateFromSource(wuuHome, source, identity string) (PendingUpdate, error) {
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()
	if strings.TrimSpace(identity) == "" {
		return PendingUpdate{}, errors.New("plugin source identity is required")
	}
	root, err := pendingUpdatesRoot(wuuHome)
	if err != nil {
		return PendingUpdate{}, err
	}
	absSource, sourceInfo, err := localPackageSource(source)
	if err != nil {
		return PendingUpdate{}, err
	}
	if sourceInfo.IsDir() && pathContains(absSource, root) {
		return PendingUpdate{}, fmt.Errorf("plugin source %s contains the pending update store", absSource)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return PendingUpdate{}, fmt.Errorf("create pending plugin update directory: %w", err)
	}

	container, err := os.MkdirTemp(root, ".stage-")
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("create pending plugin update staging directory: %w", err)
	}
	defer os.RemoveAll(container)

	generation := filepath.Join(container, "pending")
	packageRoot := filepath.Join(generation, "package")
	if err := os.Mkdir(generation, 0o755); err != nil {
		return PendingUpdate{}, fmt.Errorf("create pending plugin update generation: %w", err)
	}

	var kind PackageSourceKind
	var archiveRoot string
	if sourceInfo.IsDir() {
		kind = PackageSourceDirectory
		if err := copyPackageTree(absSource, packageRoot); err != nil {
			return PendingUpdate{}, fmt.Errorf("stage plugin update directory: %w", err)
		}
	} else {
		kind = PackageSourceZip
		item, err := extractAndInspectZip(absSource, filepath.Join(container, "archive"))
		if err != nil {
			return PendingUpdate{}, fmt.Errorf("stage plugin update zip: %w", err)
		}
		archiveRoot = item.archiveRoot
		if err := os.Rename(item.root, packageRoot); err != nil {
			return PendingUpdate{}, fmt.Errorf("store extracted plugin update: %w", err)
		}
	}

	item, err := inspectPackageTree(packageRoot)
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("validate staged plugin update: %w", err)
	}
	active, activePath, err := installedUserPlugin(wuuHome, item.plugin.ID)
	if err != nil {
		return PendingUpdate{}, err
	}
	pendingFingerprint, err := installedPackageFingerprint(item.plugin, packageRoot, activePath)
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("fingerprint staged plugin update %s: %w", item.plugin.ID, err)
	}
	if pendingFingerprint == active.Fingerprint {
		return PendingUpdate{}, fmt.Errorf("plugin update %s has the same fingerprint as the installed generation", item.plugin.ID)
	}

	destination, err := pendingUpdatePath(root, item.plugin.ID)
	if err != nil {
		return PendingUpdate{}, err
	}
	item.plugin.Fingerprint = pendingFingerprint
	inspection := inspectionFromStaged(item, destinationPackagePath(destination), kind)
	inspection.SourcePath = destinationPackagePath(destination)
	inspection.ArchiveRoot = filepath.ToSlash(archiveRoot)
	metadata := pendingUpdateMetadata{
		SourceIdentity:    identity,
		Package:           inspection,
		ActiveFingerprint: active.Fingerprint,
	}
	if err := writePendingUpdateMetadata(generation, metadata); err != nil {
		return PendingUpdate{}, err
	}

	previous := filepath.Join(container, "previous")
	_, statErr := os.Lstat(destination)
	switch {
	case statErr == nil:
		if err := os.Rename(destination, previous); err != nil {
			return PendingUpdate{}, fmt.Errorf("stage previous pending plugin update %s: %w", item.plugin.ID, err)
		}
	case os.IsNotExist(statErr):
	case statErr != nil:
		return PendingUpdate{}, fmt.Errorf("inspect pending plugin update %s: %w", item.plugin.ID, statErr)
	}
	if err := os.Rename(generation, destination); err != nil {
		if _, previousErr := os.Lstat(previous); previousErr == nil {
			if restoreErr := os.Rename(previous, destination); restoreErr != nil {
				return PendingUpdate{}, fmt.Errorf("publish pending plugin update %s: %w (restore failed: %v; previous generation remains at %s)", item.plugin.ID, err, restoreErr, previous)
			}
		}
		return PendingUpdate{}, fmt.Errorf("publish pending plugin update %s: %w", item.plugin.ID, err)
	}

	return readPendingUpdateAt(wuuHome, root, item.plugin.ID)
}

// ReadPendingUpdate returns and revalidates the pending metadata and package.
func ReadPendingUpdate(wuuHome, id string) (PendingUpdate, error) {
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()

	root, err := pendingUpdatesRoot(wuuHome)
	if err != nil {
		return PendingUpdate{}, err
	}
	return readPendingUpdateAt(wuuHome, root, id)
}

// InspectPendingUpdate is the inspection-oriented name for ReadPendingUpdate.
func InspectPendingUpdate(wuuHome, id string) (PendingUpdate, error) {
	return ReadPendingUpdate(wuuHome, id)
}

// ListPendingUpdates returns every valid pending generation sorted by plugin id.
func ListPendingUpdates(wuuHome string) ([]PendingUpdate, error) {
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()

	root, err := pendingUpdatesRoot(wuuHome)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list pending plugin updates: %w", err)
	}

	updates := make([]PendingUpdate, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() {
			return nil, fmt.Errorf("pending plugin update store contains unexpected entry %q", entry.Name())
		}
		update, err := readPendingUpdateAt(wuuHome, root, entry.Name())
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	sort.Slice(updates, func(i, j int) bool {
		return updates[i].Package.ID < updates[j].Package.ID
	})
	return updates, nil
}

// PromotePendingUpdate publishes the exact approved pending generation through
// InstallPackage, retaining its rollback behavior. A stale fingerprint leaves
// both the installed and pending generations untouched.
func PromotePendingUpdate(wuuHome, id, fingerprint string) (InstallResult, error) {
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()

	root, err := pendingUpdatesRoot(wuuHome)
	if err != nil {
		return InstallResult{}, err
	}
	pending, err := readPendingUpdateAt(wuuHome, root, id)
	if err != nil {
		return InstallResult{}, err
	}
	if fingerprint == "" || fingerprint != pending.Package.Fingerprint {
		return InstallResult{}, fmt.Errorf("%w for plugin %s", ErrPendingUpdateFingerprintMismatch, id)
	}
	active, _, err := installedUserPlugin(wuuHome, id)
	if err != nil {
		return InstallResult{}, err
	}
	if active.Fingerprint != pending.ActiveFingerprint {
		return InstallResult{}, fmt.Errorf("installed plugin %s changed after its update was staged", id)
	}

	destination, err := pendingUpdatePath(root, id)
	if err != nil {
		return InstallResult{}, err
	}
	promotionPath, err := os.MkdirTemp(root, ".promote-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("reserve plugin update promotion path: %w", err)
	}
	if err := os.Remove(promotionPath); err != nil {
		return InstallResult{}, fmt.Errorf("prepare plugin update promotion path: %w", err)
	}
	if err := os.Rename(destination, promotionPath); err != nil {
		return InstallResult{}, fmt.Errorf("claim pending plugin update %s: %w", id, err)
	}

	result, installErr := InstallPackage(wuuHome, destinationPackagePath(promotionPath))
	if installErr != nil {
		if restoreErr := os.Rename(promotionPath, destination); restoreErr != nil {
			return InstallResult{}, fmt.Errorf("promote pending plugin update %s: %w (restore pending generation: %v; generation remains at %s)", id, installErr, restoreErr, promotionPath)
		}
		return InstallResult{}, fmt.Errorf("promote pending plugin update %s: %w", id, installErr)
	}
	// Installation is the commit point. Cleanup is best-effort because returning
	// an error after InstallPackage succeeds would make callers roll back their
	// live generation even though the new package is already durable on disk.
	_ = os.RemoveAll(promotionPath)
	return result, nil
}

// PreparePendingUpdateRemoval hides the exact pending generation while keeping
// a rollback copy. A stale fingerprint leaves it untouched.
func PreparePendingUpdateRemoval(wuuHome, id, fingerprint string) (*PendingUpdateRemoval, error) {
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()

	root, err := pendingUpdatesRoot(wuuHome)
	if err != nil {
		return nil, err
	}
	pending, err := readPendingUpdateAt(wuuHome, root, id)
	if err != nil {
		return nil, err
	}
	if fingerprint == "" || fingerprint != pending.Package.Fingerprint {
		return nil, fmt.Errorf("%w for plugin %s", ErrPendingUpdateFingerprintMismatch, id)
	}
	destination, err := pendingUpdatePath(root, id)
	if err != nil {
		return nil, err
	}
	tombstone, err := os.MkdirTemp(root, ".remove-")
	if err != nil {
		return nil, fmt.Errorf("reserve removed plugin update path: %w", err)
	}
	if err := os.Remove(tombstone); err != nil {
		return nil, fmt.Errorf("prepare removed plugin update path: %w", err)
	}
	if err := os.Rename(destination, tombstone); err != nil {
		return nil, fmt.Errorf("claim removed plugin update %s: %w", id, err)
	}
	return &PendingUpdateRemoval{destination: destination, tombstone: tombstone}, nil
}

func (t *PendingUpdateRemoval) Rollback() error {
	if t == nil || t.finished {
		return nil
	}
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()
	if _, err := os.Lstat(t.destination); err == nil {
		return fmt.Errorf("restore pending plugin update: destination already exists at %s", t.destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("restore pending plugin update: inspect destination: %w", err)
	}
	if err := os.Rename(t.tombstone, t.destination); err != nil {
		return fmt.Errorf("restore pending plugin update: %w (recovery copy remains at %s)", err, t.tombstone)
	}
	t.finished = true
	return nil
}

func (t *PendingUpdateRemoval) Commit() error {
	if t == nil || t.finished {
		return nil
	}
	pendingUpdatesMu.Lock()
	defer pendingUpdatesMu.Unlock()
	t.finished = true
	if err := os.RemoveAll(t.tombstone); err != nil {
		return fmt.Errorf("remove pending plugin update tombstone %s: %w", t.tombstone, err)
	}
	return nil
}

// RejectPendingUpdate is the direct operation built on the same reversible
// transaction used by package removal.
func RejectPendingUpdate(wuuHome, id, fingerprint string) error {
	transaction, err := PreparePendingUpdateRemoval(wuuHome, id, fingerprint)
	if err != nil {
		return err
	}
	return transaction.Commit()
}

func pendingUpdatesRoot(wuuHome string) (string, error) {
	pluginsRoot, err := userInstallRoot(wuuHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(pluginsRoot), "updates", "plugins"), nil
}

func pendingUpdatePath(root, id string) (string, error) {
	if err := validateInstallID(id); err != nil {
		return "", err
	}
	destination := filepath.Join(root, id)
	if !pathContains(root, destination) {
		return "", fmt.Errorf("pending plugin update path for %q escapes its store", id)
	}
	return destination, nil
}

func destinationPackagePath(destination string) string {
	return filepath.Join(destination, "package")
}

func installedUserPlugin(wuuHome, id string) (Plugin, string, error) {
	if err := validateInstallID(id); err != nil {
		return Plugin{}, "", err
	}
	pluginsRoot, err := userInstallRoot(wuuHome)
	if err != nil {
		return Plugin{}, "", err
	}
	destination := filepath.Join(pluginsRoot, id)
	if !pathContains(pluginsRoot, destination) {
		return Plugin{}, "", fmt.Errorf("installed plugin path for %q escapes its store", id)
	}
	item, err := inspectPackageTree(destination)
	if os.IsNotExist(err) {
		return Plugin{}, "", fmt.Errorf("installed user plugin %s is required: %w", id, os.ErrNotExist)
	}
	if err != nil {
		return Plugin{}, "", fmt.Errorf("inspect installed user plugin %s: %w", id, err)
	}
	if item.plugin.ID != id {
		return Plugin{}, "", fmt.Errorf("installed user plugin path %s contains id %q", id, item.plugin.ID)
	}
	return item.plugin, destination, nil
}

func installedPackageFingerprint(item Plugin, packageRoot, installedRoot string) (string, error) {
	item.Root = packageRoot
	if item.Runtime != nil && item.RuntimePath != "" && pathContains(packageRoot, item.RuntimePath) {
		rel, err := filepath.Rel(packageRoot, item.RuntimePath)
		if err != nil {
			return "", err
		}
		item.RuntimePath = filepath.Join(installedRoot, rel)
	}
	contract, err := item.PackageContract()
	if err != nil {
		return "", err
	}
	return contract.Fingerprint, nil
}

func writePendingUpdateMetadata(generation string, metadata pendingUpdateMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pending plugin update metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(generation, pendingUpdateMetadataFilename), data, 0o600); err != nil {
		return fmt.Errorf("write pending plugin update metadata: %w", err)
	}
	return nil
}

func readPendingUpdateAt(wuuHome, root, id string) (PendingUpdate, error) {
	destination, err := pendingUpdatePath(root, id)
	if err != nil {
		return PendingUpdate{}, err
	}
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return PendingUpdate{}, fmt.Errorf("%w for plugin %s", ErrPendingUpdateNotFound, id)
	}
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("inspect pending plugin update %s: %w", id, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return PendingUpdate{}, fmt.Errorf("pending plugin update %s is not a directory", id)
	}

	metadataPath := filepath.Join(destination, pendingUpdateMetadataFilename)
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("inspect pending plugin update metadata %s: %w", id, err)
	}
	if !metadataInfo.Mode().IsRegular() || metadataInfo.Mode()&os.ModeSymlink != 0 || metadataInfo.Size() > 64<<10 {
		return PendingUpdate{}, fmt.Errorf("pending plugin update metadata %s is not a bounded regular file", id)
	}
	file, err := os.Open(metadataPath)
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("open pending plugin update metadata %s: %w", id, err)
	}
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var metadata pendingUpdateMetadata
	decodeErr := decoder.Decode(&metadata)
	if decodeErr == nil {
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			decodeErr = errors.New("metadata contains trailing data")
		}
	}
	closeErr := file.Close()
	if decodeErr != nil {
		return PendingUpdate{}, fmt.Errorf("decode pending plugin update metadata %s: %w", id, decodeErr)
	}
	if closeErr != nil {
		return PendingUpdate{}, fmt.Errorf("close pending plugin update metadata %s: %w", id, closeErr)
	}
	if metadata.Package.ID != id || metadata.ActiveFingerprint == "" || metadata.Package.Fingerprint == "" {
		return PendingUpdate{}, fmt.Errorf("pending plugin update metadata for %s is inconsistent", id)
	}

	packageRoot := destinationPackagePath(destination)
	item, err := inspectPackageTree(packageRoot)
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("validate pending plugin update %s: %w", id, err)
	}
	if item.plugin.ID != id {
		return PendingUpdate{}, fmt.Errorf("pending plugin update path %s contains id %q", id, item.plugin.ID)
	}
	pluginsRoot, err := userInstallRoot(wuuHome)
	if err != nil {
		return PendingUpdate{}, err
	}
	installedPath := filepath.Join(pluginsRoot, id)
	if !pathContains(pluginsRoot, installedPath) {
		return PendingUpdate{}, fmt.Errorf("installed plugin path for %q escapes its store", id)
	}
	fingerprint, err := installedPackageFingerprint(item.plugin, packageRoot, installedPath)
	if err != nil {
		return PendingUpdate{}, fmt.Errorf("fingerprint pending plugin update %s: %w", id, err)
	}
	if fingerprint != metadata.Package.Fingerprint {
		return PendingUpdate{}, fmt.Errorf("pending plugin update %s changed after staging", id)
	}
	metadata.Package.SourcePath = packageRoot
	return PendingUpdate{
		SourceIdentity:    metadata.SourceIdentity,
		Package:           metadata.Package,
		ActiveFingerprint: metadata.ActiveFingerprint,
		Path:              destination,
	}, nil
}
