package enginecatalog

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const (
	pathMarkerStart = "__WUU_PATH_START__"
	pathMarkerEnd   = "__WUU_PATH_END__"
)

// pathProbe is the process-wide record of whether this launch arrived without
// the user's own bin directories, plus the login-shell directories discovered
// for that case. Tests replace the pointer; production never resets it.
type pathProbe struct {
	guiOnce   sync.Once
	guiMiss   bool
	shellOnce sync.Once
	shellDirs []string
}

var (
	probeGate sync.Mutex
	probe     = &pathProbe{}

	// loadLoginShellDirs is replaced by tests. Production reads the login shell.
	loadLoginShellDirs = readLoginShellPath
)

func currentProbe() *pathProbe {
	probeGate.Lock()
	defer probeGate.Unlock()
	return probe
}

func resetPathProbeState() {
	probeGate.Lock()
	probe = &pathProbe{}
	probeGate.Unlock()
}

// LookBinary resolves name using PATH, then the install locations a desktop
// launch cannot see. Finder and Dock give the process only the system PATH
// from /etc/paths, so a CLI in ~/.local/bin, Homebrew, or a version manager
// is invisible to a plain LookPath.
//
// Callers apply settings and WUU_*_BINARY overrides first. LookBinary does
// not consult those and does not execute the program.
func LookBinary(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", exec.ErrNotFound
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if path, err := searchDirs(name, existingDirs(staticBinDirs())); err == nil {
		rememberDir(filepath.Dir(path))
		return path, nil
	}
	if guiPathNeedsShell() {
		if path, err := searchDirs(name, shellBinDirs()); err == nil {
			rememberDir(filepath.Dir(path))
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

// InstallUserPath prepends standard install locations onto PATH when this
// process has none of the user's bin directories. Child engines then resolve
// interpreters (node, mise shims) the same way a terminal would. A PATH that
// already contains a home directory or Homebrew is left alone.
func InstallUserPath() {
	if !guiPathNeedsShell() {
		return
	}
	prependMissing(existingDirs(staticBinDirs()))
	if testing.Testing() {
		return
	}
	go func() { _ = shellBinDirs() }()
}

func guiPathNeedsShell() bool {
	p := currentProbe()
	p.guiOnce.Do(func() {
		p.guiMiss = pathMissesUserBins(os.Getenv("PATH"))
	})
	return p.guiMiss
}

func shellBinDirs() []string {
	p := currentProbe()
	p.shellOnce.Do(func() {
		p.shellDirs = existingDirs(loadLoginShellDirs())
		if !testing.Testing() {
			prependMissing(p.shellDirs)
		}
	})
	return append([]string(nil), p.shellDirs...)
}

func rememberDir(dir string) {
	if testing.Testing() || strings.TrimSpace(dir) == "" {
		return
	}
	prependMissing([]string{dir})
}

func searchDirs(name string, dirs []string) (string, error) {
	for _, dir := range dirs {
		path, err := exec.LookPath(filepath.Join(dir, name))
		if err == nil {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

// staticBinDirs lists upstream install locations that are not on the system
// PATH a graphical launch receives. Order is the search order: a user-local
// install wins over Homebrew.
func staticBinDirs() []string {
	var dirs []string
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
			filepath.Join(home, ".opencode", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".proto", "shims"),
			filepath.Join(home, ".nix-profile", "bin"),
			filepath.Join(home, ".mise", "shims"),
		)
		if data := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); data != "" {
			dirs = append(dirs, filepath.Join(data, "mise", "shims"))
		} else {
			dirs = append(dirs, filepath.Join(home, ".local", "share", "mise", "shims"))
		}
		if mise := strings.TrimSpace(os.Getenv("MISE_DATA_DIR")); mise != "" {
			dirs = append(dirs, filepath.Join(mise, "shims"))
		}
		if bin := nvmBinDir(home); bin != "" {
			dirs = append(dirs, bin)
		}
	}
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			dirs = append(dirs, filepath.Join(local, "Programs"))
		}
		return dirs
	}
	dirs = append(dirs, fixedInstallRoots()...)
	return dirs
}

func fixedInstallRoots() []string {
	return []string{
		"/opt/homebrew/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"/opt/local/bin",
	}
}

func pathMissesUserBins(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	for _, dir := range filepath.SplitList(path) {
		if home != "" && dirUnder(dir, home) {
			return false
		}
		if managedInstallDir(dir) {
			return false
		}
	}
	return true
}

func managedInstallDir(dir string) bool {
	for _, root := range []string{"/opt/homebrew", "/home/linuxbrew/.linuxbrew", "/opt/local"} {
		if dir == root || dirUnder(dir, root) {
			return true
		}
	}
	return false
}

func dirUnder(dir, root string) bool {
	dir = filepath.Clean(strings.TrimSpace(dir))
	root = filepath.Clean(strings.TrimSpace(root))
	if dir == "" || root == "" || root == "." || root == string(os.PathSeparator) {
		return false
	}
	if runtime.GOOS == "windows" {
		dir = strings.ToLower(dir)
		root = strings.ToLower(root)
	}
	if dir == root {
		return true
	}
	sep := string(os.PathSeparator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(dir, root)
}

func existingDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		key := dir
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, dir)
	}
	return out
}

func prependMissing(dirs []string) {
	merged := mergePathLists(dirs, filepath.SplitList(os.Getenv("PATH")))
	next := strings.Join(merged, string(os.PathListSeparator))
	if next == os.Getenv("PATH") {
		return
	}
	_ = os.Setenv("PATH", next)
}

func parseMarkedPath(out string) []string {
	var dirs []string
	rest := out
	for {
		start := strings.Index(rest, pathMarkerStart)
		if start < 0 {
			return dirs
		}
		rest = rest[start+len(pathMarkerStart):]
		end := strings.Index(rest, pathMarkerEnd)
		if end < 0 {
			return dirs
		}
		dirs = splitAbsPathList(rest[:end])
		rest = rest[end+len(pathMarkerEnd):]
	}
}

func splitAbsPathList(value string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, dir := range strings.Split(value, string(os.PathListSeparator)) {
		dir = strings.TrimSpace(dir)
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		key := dir
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, dir)
	}
	return out
}

func mergePathLists(front, back []string) []string {
	seen := make(map[string]struct{}, len(front)+len(back))
	out := make([]string, 0, len(front)+len(back))
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		dir = filepath.Clean(dir)
		key := dir
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, dir)
	}
	for _, dir := range front {
		add(dir)
	}
	for _, dir := range back {
		add(dir)
	}
	return out
}
