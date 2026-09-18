package plugin

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/storelock"
)

//go:embed all:bundled
var bundledFS embed.FS

const bundledFingerprintFile = ".fingerprint"

// EnableCUAMacEnv gates the bundled Computer Use plugin. It remains off in
// normal releases while the native integration is under internal evaluation.
const EnableCUAMacEnv = "WUU_ENABLE_CUA_MAC"

type DiscoverOptions struct {
	GOOS       string
	WuuVersion string
	LookPath   func(string) (string, error)
	LookupEnv  func(string) (string, bool)
}

func defaultDiscoverOptions() DiscoverOptions {
	compatibility := defaultCompatibilityOptions()
	return DiscoverOptions{
		GOOS:       compatibility.GOOS,
		WuuVersion: compatibility.WuuVersion,
		LookPath:   compatibility.LookPath,
		LookupEnv:  os.LookupEnv,
	}
}

func discoverBundled(wuuHome string, options DiscoverOptions) []Plugin {
	if strings.TrimSpace(options.GOOS) == "" {
		options.GOOS = runtime.GOOS
	}
	if options.LookupEnv == nil {
		options.LookupEnv = os.LookupEnv
	}
	root, err := materializeBundled(wuuHome)
	if err != nil {
		slog.Error("materialize bundled plugins", "error", err)
		return nil
	}
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		slog.Error("read bundled plugins", "path", root, "error", err)
		return nil
	}
	var out []Plugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		item, err := loadOfficialPluginDir(filepath.Join(root, entry.Name()))
		if err != nil {
			slog.Error("load bundled plugin", "plugin", entry.Name(), "error", err)
			continue
		}
		resolveOfficialHelper(&item, options.LookupEnv)
		if ValidateHostCompatibility(item, CompatibilityOptions{
			GOOS: options.GOOS, WuuVersion: options.WuuVersion, LookPath: options.LookPath,
		}) != nil || !bundledPluginEnabled(item, options.LookupEnv) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func bundledPluginEnabled(item Plugin, lookupEnv func(string) (string, bool)) bool {
	var key string
	switch item.ID {
	case "cua-mac":
		key = EnableCUAMacEnv
	default:
		return true
	}
	if lookupEnv == nil {
		return false
	}
	value, ok := lookupEnv(key)
	return ok && strings.TrimSpace(value) == "1"
}

func loadOfficialPluginDir(dir string) (Plugin, error) {
	var manifestErr error
	for _, rel := range []string{ManifestFilename, CodexManifestFilename, ClaudeManifestFilename} {
		path := filepath.Join(dir, rel)
		if item, err := LoadManifestWithOptions(path, LoadOptions{Source: "bundled", Official: true}); err == nil {
			return item, nil
		} else if !os.IsNotExist(err) || manifestErr == nil {
			manifestErr = err
		}
	}
	return Plugin{}, manifestErr
}

func supportsPlatform(platforms []string, goos string) bool {
	if len(platforms) == 0 {
		return true
	}
	for _, platform := range platforms {
		if strings.EqualFold(strings.TrimSpace(platform), strings.TrimSpace(goos)) {
			return true
		}
	}
	return false
}

type officialHelperSpec struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Executable  string `json:"executable"`
}

func resolveOfficialHelper(item *Plugin, lookupEnv func(string) (string, bool)) {
	if item == nil || !item.Official || len(item.OfficialNativeHelper) == 0 {
		return
	}
	var spec officialHelperSpec
	if json.Unmarshal(item.OfficialNativeHelper, &spec) != nil || strings.TrimSpace(spec.ID) == "" {
		return
	}
	command := resolveHelperCommand(spec, lookupEnv)
	placeholder := "@wuu/official-helper:" + strings.TrimSpace(spec.ID)
	runtimePlaceholder := "@wuu-official-helper:" + strings.TrimSpace(spec.ID)
	if item.Runtime != nil && strings.TrimSpace(item.Runtime.Command) == runtimePlaceholder {
		item.Runtime.Command = command
	}
	for name, server := range item.MCPServers {
		if strings.TrimSpace(server.Command) == placeholder {
			server.Command = command
			item.MCPServers[name] = server
		}
	}
}

func resolveHelperCommand(spec officialHelperSpec, lookupEnv func(string) (string, bool)) string {
	if key := strings.TrimSpace(spec.Environment); key != "" && lookupEnv != nil {
		if value, ok := lookupEnv(key); ok && isExecutableFile(value) {
			return value
		}
	}
	name := strings.TrimSpace(spec.Executable)
	if name == "" {
		return "@wuu/official-helper:" + strings.TrimSpace(spec.ID)
	}
	if current, err := os.Executable(); err == nil {
		if sibling := filepath.Join(filepath.Dir(current), name); isExecutableFile(sibling) {
			return sibling
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	return name
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func materializeBundled(wuuHome string) (string, error) {
	if strings.TrimSpace(wuuHome) == "" {
		return "", nil
	}
	// Do not reuse or clean the legacy .bundled directory: an older running
	// app-server can still be reading it. Different builds likewise keep their
	// own generations so starting one cannot remove another's runtime assets.
	parent := filepath.Join(wuuHome, "cache", "plugins", ".bundled-generations")
	want := bundledFingerprint()
	dest := filepath.Join(parent, want)
	lock, err := storelock.Acquire(parent)
	if err != nil {
		return "", fmt.Errorf("lock bundled plugin cache: %w", err)
	}
	defer lock.Release()
	// A completion marker alone cannot detect a partially deleted cache.
	if bundledCacheComplete(dest, want) {
		return dest, nil
	}
	staging, err := os.MkdirTemp(parent, ".unpacking-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	err = fs.WalkDir(bundledFS, "bundled", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, "bundled"), "/")
		target := filepath.Join(staging, filepath.FromSlash(rel))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := bundledFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o755); mkdirErr != nil {
			return mkdirErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, bundledFingerprintFile), []byte(want), 0o644); err != nil {
		return "", err
	}
	// Only an incomplete generation is replaced, after its replacement is
	// ready. The cross-process lock covers validation, repair and publication.
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(staging, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func bundledCacheComplete(root, fingerprint string) bool {
	got, err := os.ReadFile(filepath.Join(root, bundledFingerprintFile))
	if err != nil || string(got) != fingerprint {
		return false
	}
	err = fs.WalkDir(bundledFS, "bundled", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel("bundled", path)
		if err != nil {
			return err
		}
		target := filepath.Join(root, rel)
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if entry.IsDir() && info.IsDir() {
			return nil
		}
		if entry.IsDir() || !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected file type: %s", target)
		}
		want, err := bundledFS.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("incomplete bundled asset: %s", target)
		}
		return nil
	})
	return err == nil
}

func bundledFingerprint() string {
	var paths []string
	_ = fs.WalkDir(bundledFS, "bundled", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		data, err := bundledFS.ReadFile(path)
		if err != nil {
			continue
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
