package plugin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxPackageEntries = 10_000
	maxPackageBytes   = int64(512 << 20)
)

var errPackageManifestMissing = errors.New("plugin package manifest is missing")

// PackageSourceKind identifies the local package representation that was
// inspected or installed.
type PackageSourceKind string

const (
	PackageSourceDirectory PackageSourceKind = "directory"
	PackageSourceZip       PackageSourceKind = "zip"
)

// PackageInspection contains stable package metadata. Paths in this value are
// relative package paths so an inspection of an archive does not expose paths
// in a temporary extraction directory.
type PackageInspection struct {
	ID                   string
	Name                 string
	Version              string
	Description          string
	SourcePath           string
	SourceKind           PackageSourceKind
	ArchiveRoot          string
	ManifestPath         string
	FileCount            int
	UnpackedSize         int64
	Fingerprint          string
	RequestedPermissions []string
	EffectivePermissions []string
	UnsupportedFields    []string
}

// InstallResult describes a package published to the user plugin directory.
type InstallResult struct {
	Package     PackageInspection
	Plugin      Plugin
	Destination string
	Replaced    bool
}

// UninstallResult describes removal from the user plugin directory. Removed is
// false when no installed package existed for the requested id.
type UninstallResult struct {
	ID          string
	Destination string
	Removed     bool
}

// UninstallTransaction keeps a removed package in a private tombstone until
// the caller commits the matching policy and runtime generation.
type UninstallTransaction struct {
	destination string
	tombstone   string
	packagePath string
	finished    bool
}

// PackToZip creates a distributable .zip from a validated plugin directory.
// It validates the source directory first, then archives it. The zip is
// suitable for distribution and installation via wuu plugin install.
func PackToZip(sourceDir, zipPath string) error {
	if _, err := InspectPackage(sourceDir); err != nil {
		return fmt.Errorf("pack: validate plugin: %w", err)
	}
	if err := zipDirectory(sourceDir, zipPath); err != nil {
		return fmt.Errorf("pack: create zip: %w", err)
	}
	return nil
}

type packageTreeStats struct {
	files int
	bytes int64
}

type stagedPackage struct {
	root        string
	manifestRel string
	archiveRoot string
	plugin      Plugin
	stats       packageTreeStats
}

// InspectPackage validates a local plugin directory or .zip without installing
// it. Inspection performs the same manifest, tree, and expansion checks used by
// InstallPackage.
func InspectPackage(source string) (PackageInspection, error) {
	absSource, info, err := localPackageSource(source)
	if err != nil {
		return PackageInspection{}, err
	}

	if info.IsDir() {
		item, err := inspectPackageTree(absSource)
		if err != nil {
			return PackageInspection{}, fmt.Errorf("inspect plugin directory: %w", err)
		}
		return inspectionFromStaged(item, absSource, PackageSourceDirectory), nil
	}

	temp, err := os.MkdirTemp("", "wuu-plugin-inspect-")
	if err != nil {
		return PackageInspection{}, fmt.Errorf("create plugin inspection directory: %w", err)
	}
	defer os.RemoveAll(temp)

	item, err := extractAndInspectZip(absSource, temp)
	if err != nil {
		return PackageInspection{}, fmt.Errorf("inspect plugin zip: %w", err)
	}
	return inspectionFromStaged(item, absSource, PackageSourceZip), nil
}

// InstallPackage validates and installs a local plugin directory or .zip under
// <wuuHome>/plugins/<plugin-id>. A complete generation is staged beside the
// destination before publication. If replacement cannot be published, the old
// generation is restored.
func InstallPackage(wuuHome, source string) (InstallResult, error) {
	pluginsRoot, err := userInstallRoot(wuuHome)
	if err != nil {
		return InstallResult{}, err
	}
	absSource, sourceInfo, err := localPackageSource(source)
	if err != nil {
		return InstallResult{}, err
	}
	if sourceInfo.IsDir() && pathContains(absSource, pluginsRoot) {
		return InstallResult{}, fmt.Errorf("plugin source %s contains the installation staging directory", absSource)
	}
	if err := os.MkdirAll(pluginsRoot, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create user plugin directory: %w", err)
	}

	container, err := os.MkdirTemp(pluginsRoot, ".install-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("create plugin staging directory: %w", err)
	}
	cleanupContainer := true
	defer func() {
		if cleanupContainer {
			_ = os.RemoveAll(container)
		}
	}()

	var item stagedPackage
	var kind PackageSourceKind
	if sourceInfo.IsDir() {
		kind = PackageSourceDirectory
		content := filepath.Join(container, "package")
		if err := copyPackageTree(absSource, content); err != nil {
			return InstallResult{}, fmt.Errorf("stage plugin directory: %w", err)
		}
		item, err = inspectPackageTree(content)
	} else {
		kind = PackageSourceZip
		item, err = extractAndInspectZip(absSource, filepath.Join(container, "archive"))
	}
	if err != nil {
		return InstallResult{}, fmt.Errorf("validate staged plugin: %w", err)
	}

	destination := filepath.Join(pluginsRoot, item.plugin.ID)
	result := InstallResult{
		Package:     inspectionFromStaged(item, absSource, kind),
		Destination: destination,
	}

	_, statErr := os.Lstat(destination)
	switch {
	case statErr == nil:
		result.Replaced = true
	case os.IsNotExist(statErr):
	case statErr != nil:
		return InstallResult{}, fmt.Errorf("inspect installed plugin %s: %w", item.plugin.ID, statErr)
	}

	backup := filepath.Join(container, "previous")
	if result.Replaced {
		if err := os.Rename(destination, backup); err != nil {
			return InstallResult{}, fmt.Errorf("stage existing plugin %s for replacement: %w", item.plugin.ID, err)
		}
	}
	if err := os.Rename(item.root, destination); err != nil {
		if result.Replaced {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				cleanupContainer = false
				return InstallResult{}, fmt.Errorf("publish plugin %s: %w (restore failed: %v; previous package remains at %s)", item.plugin.ID, err, restoreErr, backup)
			}
		}
		return InstallResult{}, fmt.Errorf("publish plugin %s: %w", item.plugin.ID, err)
	}

	installed, err := LoadManifest(filepath.Join(destination, item.manifestRel), "user")
	if err != nil {
		failed := filepath.Join(container, "failed")
		moveErr := os.Rename(destination, failed)
		var restoreErr error
		if result.Replaced && moveErr == nil {
			restoreErr = os.Rename(backup, destination)
		}
		if moveErr != nil || restoreErr != nil {
			cleanupContainer = false
			return InstallResult{}, fmt.Errorf("validate published plugin %s: %w (rollback failed: move=%v restore=%v; recovery files remain at %s)", item.plugin.ID, err, moveErr, restoreErr, container)
		}
		return InstallResult{}, fmt.Errorf("validate published plugin %s: %w", item.plugin.ID, err)
	}
	result.Plugin = installed
	result.Package.Fingerprint = installed.Fingerprint
	result.Package.EffectivePermissions = append([]string(nil), installed.EffectivePermissions...)
	return result, nil
}

// PrepareUninstallPackage removes an installed package from discovery while
// retaining a rollback copy. It never interprets id as a path.
func PrepareUninstallPackage(wuuHome, id string) (*UninstallTransaction, UninstallResult, error) {
	if err := validateInstallID(id); err != nil {
		return nil, UninstallResult{}, err
	}
	pluginsRoot, err := userInstallRoot(wuuHome)
	if err != nil {
		return nil, UninstallResult{}, err
	}
	destination := filepath.Join(pluginsRoot, id)
	result := UninstallResult{ID: id, Destination: destination}
	if _, err := os.Lstat(destination); err != nil {
		if os.IsNotExist(err) {
			return nil, result, nil
		}
		return nil, result, fmt.Errorf("inspect installed plugin %s: %w", id, err)
	}

	tombstone, err := os.MkdirTemp(pluginsRoot, ".uninstall-")
	if err != nil {
		return nil, result, fmt.Errorf("create plugin uninstall staging directory: %w", err)
	}
	removePath := filepath.Join(tombstone, "package")
	if err := os.Rename(destination, removePath); err != nil {
		_ = os.Remove(tombstone)
		return nil, result, fmt.Errorf("remove plugin %s from discovery: %w", id, err)
	}
	result.Removed = true
	return &UninstallTransaction{destination: destination, tombstone: tombstone, packagePath: removePath}, result, nil
}

// Rollback restores the package to the discovery root.
func (t *UninstallTransaction) Rollback() error {
	if t == nil || t.finished {
		return nil
	}
	if _, err := os.Lstat(t.destination); err == nil {
		return fmt.Errorf("restore plugin package: destination already exists at %s", t.destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("restore plugin package: inspect destination: %w", err)
	}
	if err := os.Rename(t.packagePath, t.destination); err != nil {
		return fmt.Errorf("restore plugin package: %w (recovery copy remains at %s)", err, t.packagePath)
	}
	t.finished = true
	if err := os.Remove(t.tombstone); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plugin uninstall tombstone: %w", err)
	}
	return nil
}

// Commit makes the removal final. Discovery already changed at prepare time;
// cleanup failure only leaves a hidden recovery tombstone.
func (t *UninstallTransaction) Commit() error {
	if t == nil || t.finished {
		return nil
	}
	t.finished = true
	if err := os.RemoveAll(t.tombstone); err != nil {
		return fmt.Errorf("remove plugin uninstall tombstone %s: %w", t.tombstone, err)
	}
	return nil
}

// UninstallPackage is the direct CLI operation built on the same reversible
// transaction used by the app server.
func UninstallPackage(wuuHome, id string) (UninstallResult, error) {
	transaction, result, err := PrepareUninstallPackage(wuuHome, id)
	if err != nil || transaction == nil {
		return result, err
	}
	// The package is already absent from discovery. Tombstone cleanup is
	// best-effort and does not change the successful uninstall result.
	_ = transaction.Commit()
	return result, nil
}

func localPackageSource(source string) (string, os.FileInfo, error) {
	if strings.TrimSpace(source) == "" {
		return "", nil, errors.New("plugin package path is required")
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return "", nil, fmt.Errorf("resolve plugin package path: %w", err)
	}
	info, err := os.Lstat(absSource)
	if err != nil {
		return "", nil, fmt.Errorf("inspect plugin package %s: %w", absSource, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("plugin package path must not be a symlink: %s", absSource)
	}
	if info.IsDir() {
		return absSource, info, nil
	}
	if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(absSource), ".zip") {
		return "", nil, fmt.Errorf("plugin package must be a directory or .zip: %s", absSource)
	}
	return absSource, info, nil
}

func userInstallRoot(wuuHome string) (string, error) {
	if strings.TrimSpace(wuuHome) == "" {
		return "", errors.New("Wuu home is required")
	}
	absHome, err := filepath.Abs(wuuHome)
	if err != nil {
		return "", fmt.Errorf("resolve Wuu home: %w", err)
	}
	return filepath.Join(absHome, "plugins"), nil
}

// ValidateInstallID verifies that id is safe as one portable package path
// component without touching the plugin directory.
func ValidateInstallID(id string) error {
	return validateInstallID(id)
}

func inspectPackageTree(root string) (stagedPackage, error) {
	stats, err := validatePackageTree(root)
	if err != nil {
		return stagedPackage{}, err
	}
	manifestRel, err := findPackageManifest(root)
	if err != nil {
		return stagedPackage{}, err
	}
	item, err := LoadManifest(filepath.Join(root, manifestRel), "user")
	if err != nil {
		return stagedPackage{}, err
	}
	if err := validateInstallID(item.ID); err != nil {
		return stagedPackage{}, err
	}
	if err := ValidateHostCompatibility(item, defaultCompatibilityOptions()); err != nil {
		return stagedPackage{}, err
	}
	return stagedPackage{root: root, manifestRel: manifestRel, plugin: item, stats: stats}, nil
}

func validatePackageTree(root string) (packageTreeStats, error) {
	var stats packageTreeStats
	entries := 0
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			if !entry.IsDir() {
				return errors.New("plugin package root is not a directory")
			}
			return nil
		}
		entries++
		if entries > maxPackageEntries {
			return fmt.Errorf("plugin package exceeds %d entries", maxPackageEntries)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin package contains symlink %s", current)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin package contains unsafe non-regular entry %s (%s)", current, info.Mode().Type())
		}
		if info.Size() < 0 || info.Size() > maxPackageBytes-stats.bytes {
			return fmt.Errorf("plugin package expands beyond %d bytes", maxPackageBytes)
		}
		stats.files++
		stats.bytes += info.Size()
		return nil
	})
	if err != nil {
		return packageTreeStats{}, err
	}
	return stats, nil
}

func findPackageManifest(root string) (string, error) {
	candidates := []string{ManifestFilename, CodexManifestFilename, ClaudeManifestFilename}
	found := make([]string, 0, 1)
	for _, candidate := range candidates {
		info, err := os.Lstat(filepath.Join(root, candidate))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("plugin manifest is not a regular file: %s", candidate)
		}
		found = append(found, candidate)
	}
	if len(found) == 0 {
		return "", fmt.Errorf("%w: %s", errPackageManifestMissing, ManifestFilename)
	}
	if len(found) > 1 {
		return "", fmt.Errorf("plugin package contains conflicting manifests: %s", strings.Join(found, ", "))
	}
	return found[0], nil
}

func validateInstallID(id string) error {
	if id == "" || id != strings.TrimSpace(id) {
		return errors.New("plugin id must be a non-empty portable path component")
	}
	if len(id) > 128 || id == "." || id == ".." || strings.ContainsAny(id, `<>:"/\|?*`) || strings.HasSuffix(id, ".") || strings.HasSuffix(id, " ") {
		return fmt.Errorf("plugin id %q is not a portable path component", id)
	}
	for _, r := range id {
		if r < 32 || r == 127 {
			return fmt.Errorf("plugin id %q is not a portable path component", id)
		}
	}
	base := strings.ToUpper(strings.SplitN(id, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return fmt.Errorf("plugin id %q is reserved on supported platforms", id)
	}
	return nil
}

func copyPackageTree(source, destination string) error {
	if err := os.Mkdir(destination, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin package contains symlink %s", current)
		}
		target := filepath.Join(destination, rel)
		if info.IsDir() {
			return os.Mkdir(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin package contains unsafe non-regular entry %s (%s)", current, info.Mode().Type())
		}
		return copyRegularFile(current, target, info.Mode().Perm())
	})
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(destination, mode)
}

func extractAndInspectZip(source, extractionRoot string) (stagedPackage, error) {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return stagedPackage{}, err
	}
	defer reader.Close()
	if len(reader.File) > maxPackageEntries {
		return stagedPackage{}, fmt.Errorf("plugin archive exceeds %d entries", maxPackageEntries)
	}
	if err := os.MkdirAll(extractionRoot, 0o755); err != nil {
		return stagedPackage{}, err
	}
	if err := extractZipFiles(reader.File, extractionRoot); err != nil {
		return stagedPackage{}, err
	}
	root, archiveRoot, err := archivePackageRoot(extractionRoot)
	if err != nil {
		return stagedPackage{}, err
	}
	item, err := inspectPackageTree(root)
	if err != nil {
		return stagedPackage{}, err
	}
	item.archiveRoot = archiveRoot
	return item, nil
}

type archivePathRecord struct {
	name     string
	dir      bool
	explicit bool
}

func extractZipFiles(files []*zip.File, root string) error {
	paths := make(map[string]archivePathRecord, len(files))
	var expanded int64
	for _, file := range files {
		name, isDir, err := safeArchivePath(file)
		if err != nil {
			return err
		}
		if err := registerArchivePath(paths, name, isDir); err != nil {
			return err
		}
		if isDir {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o755); err != nil {
				return err
			}
			continue
		}
		if file.UncompressedSize64 > uint64(maxPackageBytes) || int64(file.UncompressedSize64) > maxPackageBytes-expanded {
			return fmt.Errorf("plugin archive expands beyond %d bytes", maxPackageBytes)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			in.Close()
			return err
		}
		written, copyErr := io.Copy(out, io.LimitReader(in, maxPackageBytes-expanded+1))
		closeOutErr := out.Close()
		closeInErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutErr != nil {
			return closeOutErr
		}
		if closeInErr != nil {
			return closeInErr
		}
		if written != int64(file.UncompressedSize64) {
			return fmt.Errorf("plugin archive entry %s has inconsistent expanded size", file.Name)
		}
		expanded += written
		if err := os.Chmod(target, file.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func safeArchivePath(file *zip.File) (string, bool, error) {
	raw := file.Name
	if raw == "" || strings.ContainsRune(raw, '\x00') || strings.Contains(raw, "\\") || strings.HasPrefix(raw, "/") {
		return "", false, fmt.Errorf("plugin archive contains unsafe path %q", raw)
	}
	isDir := strings.HasSuffix(raw, "/")
	trimmed := strings.TrimSuffix(raw, "/")
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != trimmed || path.IsAbs(cleaned) {
		return "", false, fmt.Errorf("plugin archive contains traversal path %q", raw)
	}
	first := strings.SplitN(cleaned, "/", 2)[0]
	if len(first) >= 2 && first[1] == ':' {
		return "", false, fmt.Errorf("plugin archive contains unsafe volume path %q", raw)
	}
	mode := file.Mode()
	if mode&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("plugin archive contains symlink entry %q", raw)
	}
	if isDir {
		if !file.FileInfo().IsDir() {
			return "", false, fmt.Errorf("plugin archive contains conflicting directory entry %q", raw)
		}
		return cleaned, true, nil
	}
	if !mode.IsRegular() {
		return "", false, fmt.Errorf("plugin archive contains unsafe non-regular entry %q (%s)", raw, mode.Type())
	}
	return cleaned, false, nil
}

func registerArchivePath(paths map[string]archivePathRecord, name string, isDir bool) error {
	parts := strings.Split(name, "/")
	for index := 1; index < len(parts); index++ {
		ancestor := strings.Join(parts[:index], "/")
		key := strings.ToLower(ancestor)
		if existing, ok := paths[key]; ok {
			if existing.name != ancestor || !existing.dir {
				return fmt.Errorf("plugin archive contains conflicting paths %q and %q", existing.name, name)
			}
		} else {
			paths[key] = archivePathRecord{name: ancestor, dir: true}
		}
	}
	key := strings.ToLower(name)
	if existing, ok := paths[key]; ok {
		if existing.name != name || existing.explicit || !existing.dir || !isDir {
			return fmt.Errorf("plugin archive contains duplicate or conflicting path %q", name)
		}
		existing.explicit = true
		paths[key] = existing
		return nil
	}
	paths[key] = archivePathRecord{name: name, dir: isDir, explicit: true}
	return nil
}

func archivePackageRoot(extractionRoot string) (string, string, error) {
	_, directErr := findPackageManifest(extractionRoot)
	direct := directErr == nil
	if directErr != nil && !errors.Is(directErr, errPackageManifestMissing) {
		return "", "", directErr
	}

	entries, err := os.ReadDir(extractionRoot)
	if err != nil {
		return "", "", err
	}
	var childRoots []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(extractionRoot, entry.Name())
		if _, err := findPackageManifest(child); err == nil {
			childRoots = append(childRoots, entry.Name())
		} else if !errors.Is(err, errPackageManifestMissing) {
			return "", "", err
		}
	}
	if (direct && len(childRoots) > 0) || len(childRoots) > 1 {
		return "", "", errors.New("plugin archive contains duplicate or conflicting package roots")
	}
	if direct {
		return extractionRoot, "", nil
	}
	if len(childRoots) == 0 {
		return "", "", fmt.Errorf("plugin archive is missing %s", ManifestFilename)
	}
	if len(entries) != 1 || entries[0].Name() != childRoots[0] {
		return "", "", errors.New("plugin archive contains files outside its package root")
	}
	return filepath.Join(extractionRoot, childRoots[0]), childRoots[0], nil
}

func inspectionFromStaged(item stagedPackage, source string, kind PackageSourceKind) PackageInspection {
	manifestPath := item.manifestRel
	if item.archiveRoot != "" {
		manifestPath = filepath.Join(item.archiveRoot, manifestPath)
	}
	return PackageInspection{
		ID:                   item.plugin.ID,
		Name:                 item.plugin.Name,
		Version:              item.plugin.Version,
		Description:          item.plugin.Description,
		SourcePath:           source,
		SourceKind:           kind,
		ArchiveRoot:          filepath.ToSlash(item.archiveRoot),
		ManifestPath:         filepath.ToSlash(manifestPath),
		FileCount:            item.stats.files,
		UnpackedSize:         item.stats.bytes,
		Fingerprint:          item.plugin.Fingerprint,
		RequestedPermissions: append([]string(nil), item.plugin.RequestedPermissions...),
		EffectivePermissions: append([]string(nil), item.plugin.EffectivePermissions...),
		UnsupportedFields:    append([]string(nil), item.plugin.UnsupportedFields...),
	}
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// zipDirectory creates a zip file containing the directory tree at source.
func zipDirectory(source, dest string) error {
	file, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create zip file: %w", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	absSource, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	return filepath.WalkDir(absSource, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(absSource, path)
		if err != nil {
			return fmt.Errorf("resolve relative path for %s: %w", path, err)
		}

		// Skip hidden files and directories (but keep dirs for traversal).
		if strings.HasPrefix(filepath.Base(rel), ".") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("get file info for %s: %w", rel, err)
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return fmt.Errorf("create zip header for %s: %w", rel, err)
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate

		entry, err := writer.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("create zip entry for %s: %w", rel, err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", rel, err)
		}

		if _, err := entry.Write(data); err != nil {
			return fmt.Errorf("write zip entry %s: %w", rel, err)
		}

		return nil
	})
}

// PackageSourceIdentity identifies a local distribution location, not its bytes.
// Updating a zip at the same canonical path is a same-source update. Retargeting
// a symlink changes the identity and therefore requires a source-change decision.
func PackageSourceIdentity(source string) (string, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// RollbackInstallation removes only the exact newly installed generation while
// the caller still holds the catalog mutation lease.
func RollbackInstallation(home, id, fingerprint string) error {
	installed, _, err := installedUserPlugin(home, id)
	if err != nil {
		return err
	}
	if installed.Fingerprint != fingerprint {
		return fmt.Errorf("plugin %q changed during failed installation; preserving package", id)
	}
	_, err = UninstallPackage(home, id)
	return err
}
