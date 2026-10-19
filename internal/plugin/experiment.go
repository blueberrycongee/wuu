package plugin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/session"
)

// ExperimentCandidate identifies an immutable prepared extension package. Its
// candidate ID addresses the manifest bytes, including source and baseline;
// Fingerprint is the distinct identity used by the ordinary runtime host.
// SourceSHA256 hashes the prepared file paths and bytes, including generated
// adapters and the embedded SDK where applicable. It is not a source Git SHA.
type ExperimentCandidate struct {
	CandidateID     string    `json:"candidate_id,omitempty"`
	ID              string    `json:"id"`
	Fingerprint     string    `json:"fingerprint"`
	SourcePath      string    `json:"source_path"`
	SourceKind      string    `json:"source_kind"`
	SourceSHA256    string    `json:"source_sha256"`
	BaseFingerprint string    `json:"base_fingerprint"`
	Workspace       string    `json:"workspace"`
	CreatedAt       time.Time `json:"created_at"`
	PackageRoot     string    `json:"package_root,omitempty"`
}

// ExperimentSelection records catalog publication, not activation in every
// conversation. The existing generation watcher and idle boundary own adoption.
type ExperimentSelection struct {
	Candidate           ExperimentCandidate `json:"candidate"`
	PreviousFingerprint string              `json:"previous_fingerprint"`
	Epoch               uint64              `json:"epoch"`
}

// CaptureExperimentCandidate snapshots prepared code without publishing it,
// authorizing a production development source, or advancing the live epoch.
// Preparation may execute trusted code; its caller owns that authorization.
func CaptureExperimentCandidate(home, workspace, sourcePath, preparedPackage string) (ExperimentCandidate, error) {
	var candidate ExperimentCandidate
	var err error
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return candidate, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return candidate, err
	}
	sourcePath, err = filepath.Abs(sourcePath)
	if err != nil {
		return candidate, err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return candidate, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return candidate, errors.New("experiment source must be a regular file or directory")
	}
	sourcePath, err = filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return candidate, err
	}
	if !pathContains(workspace, sourcePath) {
		return candidate, errors.New("experiment source must be inside its workspace")
	}
	root, err := experimentCandidateRoot(home, "")
	if err != nil {
		return candidate, err
	}
	if err := rejectExperimentSymlinks(home, root); err != nil {
		return candidate, err
	}
	if err := securefs.Mkdir(root); err != nil {
		return candidate, err
	}
	if err := rejectExperimentSymlinks(home, root); err != nil {
		return candidate, err
	}
	staging, err := os.MkdirTemp(root, ".capture-")
	if err != nil {
		return candidate, err
	}
	defer os.RemoveAll(staging)
	installed, err := InstallPackage(filepath.Join(staging, "staging"), preparedPackage)
	if err != nil {
		return candidate, fmt.Errorf("snapshot experiment package: %w", err)
	}
	packageRoot := filepath.Join(staging, "package")
	if err := os.Rename(installed.Destination, packageRoot); err != nil {
		return candidate, err
	}
	if err := os.RemoveAll(filepath.Join(staging, "staging")); err != nil {
		return candidate, err
	}
	item, sourceHash, err := inspectExperimentPackage(packageRoot)
	if err != nil {
		return candidate, err
	}
	lease, acquired, err := session.TryAcquirePluginCatalogReadLease(home)
	if err != nil {
		return candidate, err
	}
	if !acquired {
		return candidate, errors.New("plugin catalog is changing; retry candidate capture")
	}
	baseline := experimentCurrentFingerprint(home, workspace, item.ID)
	if err := lease.Release(); err != nil {
		return candidate, err
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	candidate = ExperimentCandidate{
		ID: item.ID, Fingerprint: item.Fingerprint, SourcePath: sourcePath,
		SourceKind: kind, SourceSHA256: sourceHash, BaseFingerprint: baseline,
		Workspace: workspace, CreatedAt: time.Now().UTC(),
	}
	manifest, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return ExperimentCandidate{}, err
	}
	manifest = append(manifest, '\n')
	digest := sha256.Sum256(manifest)
	candidateID := hex.EncodeToString(digest[:])
	if err := securefs.WriteFileAtomic(filepath.Join(staging, "candidate.json"), manifest); err != nil {
		return ExperimentCandidate{}, err
	}
	parent, err := experimentCandidateRoot(home, item.ID)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	if err := rejectExperimentSymlinks(home, parent); err != nil {
		return ExperimentCandidate{}, err
	}
	if err := securefs.Mkdir(parent); err != nil {
		return ExperimentCandidate{}, err
	}
	if err := rejectExperimentSymlinks(home, parent); err != nil {
		return ExperimentCandidate{}, err
	}
	if err := os.Rename(staging, filepath.Join(parent, candidateID)); err != nil {
		return ExperimentCandidate{}, fmt.Errorf("publish experiment snapshot: %w", err)
	}
	return ReadExperimentCandidate(home, item.ID, candidateID)
}

// ReadExperimentCandidate checks both immutable metadata and all package bytes.
// Symlinks and non-regular entries are rejected before any package is loaded.
func ReadExperimentCandidate(home, id, candidateID string) (ExperimentCandidate, error) {
	var candidate ExperimentCandidate
	if err := validateExperimentCandidateID(candidateID); err != nil {
		return candidate, err
	}
	root, err := experimentCandidateRoot(home, id)
	if err != nil {
		return candidate, err
	}
	if id == "" {
		return candidate, errors.New("plugin id is required")
	}
	container := filepath.Join(root, candidateID)
	if err := rejectExperimentSymlinks(home, filepath.Join(container, "candidate.json")); err != nil {
		return candidate, err
	}
	info, err := os.Lstat(filepath.Join(container, "candidate.json"))
	if err != nil {
		return candidate, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16*1024 {
		return candidate, errors.New("experiment candidate metadata must be a regular file of at most 16 KiB")
	}
	manifest, err := os.ReadFile(filepath.Join(container, "candidate.json"))
	if err != nil {
		return candidate, err
	}
	digest := sha256.Sum256(manifest)
	if hex.EncodeToString(digest[:]) != candidateID {
		return candidate, errors.New("experiment candidate metadata does not match its content address")
	}
	if err := json.Unmarshal(manifest, &candidate); err != nil {
		return candidate, err
	}
	if candidate.ID != id || candidate.CandidateID != "" || candidate.PackageRoot != "" ||
		!filepath.IsAbs(candidate.SourcePath) || !filepath.IsAbs(candidate.Workspace) ||
		(candidate.SourceKind != "file" && candidate.SourceKind != "directory") {
		return ExperimentCandidate{}, errors.New("experiment candidate metadata is invalid")
	}
	packageRoot := filepath.Join(container, "package")
	if err := rejectExperimentSymlinks(home, packageRoot); err != nil {
		return ExperimentCandidate{}, err
	}
	item, sourceHash, err := inspectExperimentPackage(packageRoot)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	if item.ID != id || item.Fingerprint != candidate.Fingerprint || sourceHash != candidate.SourceSHA256 {
		return ExperimentCandidate{}, errors.New("experiment candidate package changed after capture")
	}
	candidate.CandidateID, candidate.PackageRoot = candidateID, packageRoot
	return candidate, nil
}

// ListExperimentCandidates accepts an empty id to enumerate all captured
// packages. Corrupt records fail visibly instead of becoming usable candidates.
func ListExperimentCandidates(home, id string) ([]ExperimentCandidate, error) {
	root, err := experimentCandidateRoot(home, id)
	if err != nil {
		return nil, err
	}
	if err := rejectExperimentSymlinks(home, root); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []ExperimentCandidate{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := []ExperimentCandidate{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil, fmt.Errorf("invalid experiment directory entry: %s", entry.Name())
		}
		if id == "" {
			children, err := ListExperimentCandidates(home, entry.Name())
			if err != nil {
				return nil, err
			}
			items = append(items, children...)
			continue
		}
		candidate, err := ReadExperimentCandidate(home, id, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read experiment %s: %w", entry.Name(), err)
		}
		items = append(items, candidate)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

// ValidateExperimentSelection checks immutable bytes, workspace, current source
// authorization and the live baseline before a caller executes readiness checks.
// SelectExperimentCandidate repeats the checks under its publication writer lock.
func ValidateExperimentSelection(home, workspace, id, candidateID, expectedCurrent string) (ExperimentCandidate, error) {
	lease, acquired, err := session.TryAcquirePluginCatalogReadLease(home)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	if !acquired {
		return ExperimentCandidate{}, errors.New("plugin catalog is changing; retry candidate validation")
	}
	defer lease.Release()
	candidate, _, err := validateExperimentSelection(home, workspace, id, candidateID, expectedCurrent)
	return candidate, err
}

func validateExperimentSelection(home, workspace, id, candidateID, expectedCurrent string) (ExperimentCandidate, DevAuthorization, error) {
	var authorization DevAuthorization
	candidate, err := ReadExperimentCandidate(home, id, candidateID)
	if err != nil {
		return candidate, authorization, err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return candidate, authorization, err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return candidate, authorization, err
	}
	if candidate.Workspace != workspace || !pathContains(workspace, candidate.SourcePath) {
		return candidate, authorization, errors.New("candidate belongs to a different workspace")
	}
	current := experimentCurrentFingerprint(home, workspace, id)
	if current != expectedCurrent {
		return candidate, authorization, fmt.Errorf("candidate selection conflict: expected current fingerprint %q, observed %q; inspect status before selecting again", expectedCurrent, current)
	}
	authorization, err = ReadDevAuthorization(home, id)
	if errors.Is(err, os.ErrNotExist) {
		return candidate, authorization, fmt.Errorf("candidate source has no production development authorization; explicitly authorize its original source with wuu plugin dev before selecting archived versions: %w", err)
	}
	if err != nil {
		return candidate, authorization, err
	}
	if authorization.Directory != candidate.SourcePath ||
		(authorization.SourceKind != candidate.SourceKind && !(authorization.SourceKind == "" && candidate.SourceKind == "directory")) {
		return candidate, authorization, errors.New("candidate source differs from the authorized development source")
	}
	return candidate, authorization, nil
}

// SelectExperimentCandidate publishes an exact archived package through the
// existing development lifecycle. Its caller must first check startup readiness
// in an isolated host. expectedCurrent is mandatory at the caller; an explicitly
// empty value means the plugin must be absent. Selecting an older candidate
// rolls back code only, not storage, effects, or active turn history.
func SelectExperimentCandidate(home, workspace, id, candidateID, expectedCurrent string) (ExperimentSelection, error) {
	var selection ExperimentSelection
	lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(home)
	if err != nil {
		return selection, err
	}
	if !acquired {
		return selection, errors.New("plugin catalog is changing; retry candidate selection")
	}
	defer lease.Release()
	candidate, authorization, err := validateExperimentSelection(home, workspace, id, candidateID, expectedCurrent)
	if err != nil {
		return selection, err
	}
	_, err = publishDevGeneration(home, candidate.SourcePath, candidate.PackageRoot, authorization, candidate.Fingerprint)
	if err != nil {
		return selection, err
	}
	selection = ExperimentSelection{Candidate: candidate, PreviousFingerprint: expectedCurrent}
	selection.Epoch, err = lease.Advance()
	if err != nil {
		return selection, fmt.Errorf("candidate published but generation signal failed; inspect live status: %w", err)
	}
	return selection, lease.Release()
}

// PrepareExperimentRun creates a fresh Wuu home containing only the candidate
// development package. It copies no production configuration, storage or
// credentials. This separates Wuu state; it is not an OS or network sandbox.
func PrepareExperimentRun(home, id, candidateID, runHome string) (ExperimentCandidate, error) {
	candidate, err := ReadExperimentCandidate(home, id, candidateID)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	if err := os.Mkdir(runHome, 0o700); err != nil {
		return ExperimentCandidate{}, fmt.Errorf("experiment run requires a new private Wuu home: %w", err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return ExperimentCandidate{}, err
	}
	authorization := DevAuthorization{
		PluginID: id, Directory: candidate.SourcePath, SourceKind: candidate.SourceKind,
		Token: hex.EncodeToString(token), CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(authorization)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	authPath, err := DevAuthorizationPath(runHome, id)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	if err := securefs.WriteFileAtomic(authPath, data); err != nil {
		return ExperimentCandidate{}, err
	}
	published, err := publishDevGeneration(runHome, candidate.SourcePath, candidate.PackageRoot, authorization, candidate.Fingerprint)
	if err != nil {
		return ExperimentCandidate{}, err
	}
	candidate.PackageRoot = published.Root
	return candidate, nil
}

func experimentCandidateRoot(home, id string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("Wuu home is required")
	}
	if id != "" {
		if err := validateInstallID(id); err != nil {
			return "", err
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "experiments", "candidates", id), nil
}

func validateExperimentCandidateID(id string) error {
	digest, err := hex.DecodeString(id)
	if err != nil || len(digest) != sha256.Size || id != strings.ToLower(id) {
		return errors.New("candidate_id must be the complete lowercase SHA-256 content address")
	}
	return nil
}

func rejectExperimentSymlinks(home, path string) error {
	home, err := filepath.Abs(home)
	if err != nil {
		return err
	}
	if !pathContains(home, path) {
		return errors.New("experiment path escapes Wuu home")
	}
	for current := path; current != home; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("experiment path must not follow a symlink: %s", current)
		}
	}
	return nil
}

func inspectExperimentPackage(root string) (Plugin, string, error) {
	if _, err := validatePackageTree(root); err != nil {
		return Plugin{}, "", err
	}
	manifest, err := findPackageManifest(root)
	if err != nil {
		return Plugin{}, "", err
	}
	item, err := LoadManifest(filepath.Join(root, manifest), "dev")
	if err != nil {
		return Plugin{}, "", err
	}
	entries, err := hashPackageEntries(root, []string{"."})
	if err != nil {
		return Plugin{}, "", err
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return Plugin{}, "", err
	}
	digest := sha256.Sum256(data)
	return item, hex.EncodeToString(digest[:]), nil
}

func experimentCurrentFingerprint(home, workspace, id string) string {
	for _, item := range Discover(workspace, home) {
		if item.ID == id {
			return item.Fingerprint
		}
	}
	return ""
}
