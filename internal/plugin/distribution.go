package plugin

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	DistributionLockFilename     = ".wuu-package-lock.json"
	defaultPackageRegistry       = "https://registry.npmjs.org"
	maxDistributionMetadataBytes = 4 << 20
	maxDistributionDownloadBytes = 128 << 20
)

var npmPackageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)

// DistributionOptions applies only to explicitly requested npm sources. Wuu
// deliberately does not read npm configuration, credentials or install scripts.
type DistributionOptions struct {
	Registry  string
	Integrity string
}

// DistributionLock pins a self-contained published artifact, not an attestation
// of its author or safety. Source trust is registry + name, independent of version.
type DistributionLock struct {
	LockVersion int    `json:"lock_version"`
	Registry    string `json:"registry"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	PluginID    string `json:"plugin_id"`
	Tarball     string `json:"tarball"`
	Integrity   string `json:"integrity"`
}

// ResolvedPackage owns a private snapshot for remote sources. Call Close after
// inspection, installation or staging; it never deletes a local source.
type ResolvedPackage struct {
	Path       string
	Identity   string
	Inspection PackageInspection
	cleanup    string
}

func (p *ResolvedPackage) Close() {
	if p.cleanup != "" {
		_ = os.RemoveAll(p.cleanup)
	}
}

// ResolvePackageSource prepares a local directory/zip, exact npm package, or a
// lock:<path> artifact replay. It never installs, executes code, grants trust or
// changes an active generation. Downloads finish before the mutation lease.
func ResolvePackageSource(ctx context.Context, source string, options DistributionOptions) (*ResolvedPackage, error) {
	return resolvePackageSource(ctx, source, options, &http.Client{Timeout: 2 * time.Minute})
}

func resolvePackageSource(ctx context.Context, source string, options DistributionOptions, client *http.Client) (_ *ResolvedPackage, retErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var lock DistributionLock
	switch {
	case strings.HasPrefix(source, "npm:"):
		name, version, err := parseNPMSource(strings.TrimPrefix(source, "npm:"))
		if err != nil {
			return nil, err
		}
		registry := options.Registry
		if registry == "" {
			registry = defaultPackageRegistry
		}
		registryURL, err := distributionURL(registry)
		if err != nil {
			return nil, fmt.Errorf("package registry: %w", err)
		}
		registryURL.Path = strings.TrimRight(registryURL.Path, "/")
		registryURL.RawPath = ""
		registry = registryURL.String()
		metadataURL := registry + "/" + url.PathEscape(name) + "/" + url.PathEscape(version)
		body, err := distributionGet(ctx, client, registryURL, metadataURL, maxDistributionMetadataBytes)
		if err != nil {
			return nil, fmt.Errorf("resolve npm package: %w", err)
		}
		var metadata struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Dist    struct {
				Tarball   string `json:"tarball"`
				Integrity string `json:"integrity"`
			} `json:"dist"`
		}
		if err := json.Unmarshal(body, &metadata); err != nil {
			return nil, fmt.Errorf("decode npm package metadata: %w", err)
		}
		if metadata.Name != name || metadata.Version != version {
			return nil, errors.New("npm metadata identity does not match the requested name and version")
		}
		lock = DistributionLock{LockVersion: 1, Registry: registry, Name: name, Version: version, Tarball: metadata.Dist.Tarball, Integrity: metadata.Dist.Integrity}
		if options.Integrity != "" && options.Integrity != lock.Integrity {
			return nil, errors.New("registry integrity differs from the requested integrity pin")
		}
	case strings.HasPrefix(source, "lock:"):
		if options.Registry != "" || options.Integrity != "" {
			return nil, errors.New("lock replay does not accept registry or integrity overrides")
		}
		pinned, err := readDistributionLock(strings.TrimPrefix(source, "lock:"))
		if err != nil {
			return nil, err
		}
		lock = *pinned
	default:
		if options.Registry != "" || options.Integrity != "" {
			return nil, errors.New("registry and integrity options require an npm source")
		}
		inspection, err := InspectPackage(source)
		if err != nil {
			return nil, err
		}
		identity, err := PackageSourceIdentity(source)
		if err != nil {
			return nil, err
		}
		return &ResolvedPackage{Path: identity, Identity: identity, Inspection: inspection}, nil
	}
	if lock.LockVersion != 1 {
		return nil, fmt.Errorf("unsupported distribution lock version %d", lock.LockVersion)
	}
	if _, _, err := parseNPMSource(lock.Name + "@" + lock.Version); err != nil {
		return nil, err
	}
	registry, err := distributionURL(lock.Registry)
	if err != nil {
		return nil, err
	}
	expected, err := distributionIntegrity(lock.Integrity)
	if err != nil {
		return nil, err
	}
	archive, err := distributionGet(ctx, client, registry, lock.Tarball, maxDistributionDownloadBytes)
	if err != nil {
		return nil, fmt.Errorf("download npm artifact: %w", err)
	}
	actual := sha512.Sum512(archive)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return nil, errors.New("npm artifact integrity mismatch")
	}
	root, err := os.MkdirTemp("", "wuu-plugin-distribution-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = os.RemoveAll(root)
		}
	}()
	if err := extractDistributionTar(ctx, archive, root); err != nil {
		return nil, err
	}
	packageRoot := filepath.Join(root, "package")
	packageFile, err := os.Open(filepath.Join(packageRoot, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("read npm package identity: %w", err)
	}
	packageJSON, readErr := io.ReadAll(io.LimitReader(packageFile, maxDistributionMetadataBytes+1))
	closeErr := packageFile.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(packageJSON) > maxDistributionMetadataBytes {
		return nil, errors.New("npm package identity exceeds size limit")
	}
	var manifest struct {
		Name                 string                     `json:"name"`
		Version              string                     `json:"version"`
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
	}
	if err := json.Unmarshal(packageJSON, &manifest); err != nil {
		return nil, fmt.Errorf("read npm package identity: %w", err)
	}
	if manifest.Name != lock.Name || manifest.Version != lock.Version {
		return nil, errors.New("npm artifact identity differs from the pinned package")
	}
	if len(manifest.Dependencies)+len(manifest.OptionalDependencies)+len(manifest.PeerDependencies) != 0 {
		return nil, errors.New("distributed plugins must be self-contained: bundle runtime dependencies before publishing; Wuu does not install npm dependencies")
	}
	inspection, err := InspectPackage(packageRoot)
	if err != nil {
		return nil, err
	}
	if inspection.Version != lock.Version {
		return nil, errors.New("plugin manifest version differs from the npm package version")
	}
	if lock.PluginID != "" && inspection.ID != lock.PluginID {
		return nil, errors.New("plugin identity differs from the distribution lock")
	}
	lock.PluginID = inspection.ID
	encoded, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(filepath.Join(packageRoot, DistributionLockFilename), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("write reserved distribution lock: %w", err)
	}
	_, writeErr := lockFile.Write(append(encoded, '\n'))
	closeErr = lockFile.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return nil, err
	}
	inspection, err = InspectPackage(packageRoot)
	if err != nil {
		return nil, err
	}
	inspection.SourcePath = source
	return &ResolvedPackage{Path: packageRoot, Identity: "npm:" + strings.TrimRight(lock.Registry, "/") + "/" + lock.Name, Inspection: inspection, cleanup: root}, nil
}

func parseNPMSource(source string) (string, string, error) {
	index := strings.LastIndexByte(source, '@')
	if index <= 0 {
		return "", "", errors.New("npm source requires name@exact-version (tags and ranges are unsupported)")
	}
	name, version := source[:index], source[index+1:]
	base := strings.SplitN(strings.SplitN(version, "-", 2)[0], "+", 2)[0]
	if len(name) > 214 || !npmPackageName.MatchString(name) || strings.HasPrefix(version, "v") || strings.Count(base, ".") != 2 || !semver.IsValid("v"+version) {
		return "", "", errors.New("npm source requires a valid package name and exact semantic version")
	}
	return name, version, nil
}

func readDistributionLock(path string) (*DistributionLock, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDistributionMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDistributionMetadataBytes {
		return nil, errors.New("distribution lock exceeds size limit")
	}
	var lock DistributionLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("decode distribution lock: %w", err)
	}
	if lock.LockVersion != 1 {
		return nil, fmt.Errorf("unsupported distribution lock version %d", lock.LockVersion)
	}
	if err := validateInstallID(lock.PluginID); err != nil {
		return nil, err
	}
	if _, _, err := parseNPMSource(lock.Name + "@" + lock.Version); err != nil {
		return nil, err
	}
	registry, err := distributionURL(lock.Registry)
	if err != nil {
		return nil, err
	}
	tarball, err := distributionURL(lock.Tarball)
	if err != nil {
		return nil, err
	}
	if registry.Host != tarball.Host {
		return nil, errors.New("distribution lock tarball crosses the registry origin")
	}
	if _, err := distributionIntegrity(lock.Integrity); err != nil {
		return nil, err
	}
	return &lock, nil
}

func distributionURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid package URL")
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("package URL must use HTTPS without credentials, query or fragment")
	}
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func distributionIntegrity(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "sha512-") {
		return nil, errors.New("npm artifact requires a single SHA-512 integrity pin")
	}
	digest, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "sha512-"))
	if err != nil || len(digest) != sha512.Size {
		return nil, errors.New("invalid npm artifact SHA-512 integrity pin")
	}
	return digest, nil
}

func distributionGet(ctx context.Context, client *http.Client, registry *url.URL, target string, limit int64) ([]byte, error) {
	validate := func(raw string) error {
		u, err := distributionURL(raw)
		if err != nil {
			return err
		}
		if u.Host != registry.Host {
			return errors.New("package download or redirect crosses the configured registry origin")
		}
		return nil
	}
	if err := validate(target); err != nil {
		return nil, err
	}
	// Copy the transport for test/private CA support, but never inherit cookies or
	// redirect behavior that could broaden the explicit registry trust boundary.
	bounded := *client
	bounded.Jar = nil
	bounded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many package redirects")
		}
		return validate(req.URL.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/octet-stream")
	response, err := bounded.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("package response exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("package response exceeds %d bytes", limit)
	}
	return data, nil
}

func extractDistributionTar(ctx context.Context, data []byte, root string) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("read npm archive: %w", err)
	}
	defer reader.Close()
	archive := tar.NewReader(reader)
	paths := map[string]archivePathRecord{}
	var expanded int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if err == io.EOF {
			// tar stops at its end marker before gzip necessarily validates its
			// trailer. Drain a bounded remainder so a bad checksum cannot pass.
			remainder, err := io.Copy(io.Discard, io.LimitReader(reader, maxPackageBytes-expanded+1))
			if err != nil {
				return err
			}
			if remainder > maxPackageBytes-expanded {
				return fmt.Errorf("plugin archive expands beyond %d bytes", maxPackageBytes)
			}
			return nil
		}
		if err != nil {
			return err
		}
		if count >= maxPackageEntries {
			return fmt.Errorf("plugin archive exceeds %d entries", maxPackageEntries)
		}
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
			return fmt.Errorf("npm archive contains unsafe entry %q", header.Name)
		}
		// Reuse ZIP's portable path and case-collision rules for both formats.
		entry := &zip.File{FileHeader: zip.FileHeader{Name: header.Name}}
		mode := os.FileMode(header.Mode) & 0o777
		if header.Typeflag == tar.TypeDir {
			mode |= os.ModeDir
			entry.Name = strings.TrimSuffix(entry.Name, "/") + "/"
		}
		entry.SetMode(mode)
		name, isDir, err := safeArchivePath(entry)
		if err != nil {
			return err
		}
		if name != "package" && !strings.HasPrefix(name, "package/") {
			return errors.New("npm archive entries must remain inside package/")
		}
		if err := registerArchivePath(paths, name, isDir); err != nil {
			return err
		}
		if strings.EqualFold(name, "package/"+DistributionLockFilename) {
			return errors.New("npm archive contains reserved distribution lock")
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if header.Size < 0 || header.Size > maxPackageBytes-expanded {
			return fmt.Errorf("plugin archive expands beyond %d bytes", maxPackageBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(file, archive)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if written != header.Size {
			return errors.New("npm archive has inconsistent expanded size")
		}
		expanded += written
	}
}
