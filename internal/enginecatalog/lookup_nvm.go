package enginecatalog

import (
	"os"
	"path/filepath"
	"strings"
)

// nvmBinDir is the bin directory of the default nvm alias. Graphical launches
// do not source nvm.sh, so the active Node prefix never appears on PATH.
// Only the default alias is consulted; an arbitrary installed version is not
// a guess at the toolchain the user actually selected.
func nvmBinDir(home string) string {
	root := strings.TrimSpace(os.Getenv("NVM_DIR"))
	if root == "" && home != "" {
		root = filepath.Join(home, ".nvm")
	}
	if root == "" {
		return ""
	}
	version := resolveNvmAlias(root, "default", 0)
	if version == "" {
		return ""
	}
	return filepath.Join(root, "versions", "node", version, "bin")
}

func resolveNvmAlias(root, name string, depth int) string {
	if depth > 8 {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") {
		return ""
	}
	normalized := normalizeNodeVersion(name)
	if isConcreteNodeVersion(normalized) {
		if nodeVersionExists(root, normalized) {
			return normalized
		}
		return ""
	}
	if next, ok := readNvmAlias(root, name); ok {
		return resolveNvmAlias(root, next, depth+1)
	}
	return matchNodeVersion(root, normalized)
}

func readNvmAlias(root, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") {
		return "", false
	}
	var path string
	if strings.HasPrefix(name, "lts/") {
		leaf := strings.TrimPrefix(name, "lts/")
		if leaf == "" || strings.ContainsAny(leaf, `/\`) {
			return "", false
		}
		path = filepath.Join(root, "alias", "lts", leaf)
	} else {
		if strings.ContainsAny(name, `/\`) {
			return "", false
		}
		path = filepath.Join(root, "alias", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	next := strings.TrimSpace(string(data))
	if next == "" {
		return "", false
	}
	return next, true
}

func nodeVersionExists(root, version string) bool {
	info, err := os.Stat(filepath.Join(root, "versions", "node", version))
	return err == nil && info.IsDir()
}

func matchNodeVersion(root, prefix string) string {
	prefix = normalizeNodeVersion(prefix)
	if !numericNodePrefix(prefix) {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(root, "versions", "node"))
	if err != nil {
		return ""
	}
	best := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name != prefix && !strings.HasPrefix(name, prefix+".") {
			continue
		}
		if best == "" || compareNodeVersion(name, best) > 0 {
			best = name
		}
	}
	return best
}

func normalizeNodeVersion(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if name[0] >= '0' && name[0] <= '9' {
		return "v" + name
	}
	return name
}

func isConcreteNodeVersion(name string) bool {
	name = strings.TrimPrefix(name, "v")
	parts := strings.Split(name, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || !digitsOnly(part) {
			return false
		}
	}
	return true
}

func numericNodePrefix(prefix string) bool {
	if !strings.HasPrefix(prefix, "v") {
		return false
	}
	rest := strings.TrimPrefix(prefix, "v")
	if rest == "" {
		return false
	}
	for _, r := range rest {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func digitsOnly(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareNodeVersion(a, b string) int {
	av := versionParts(a)
	bv := versionParts(b)
	n := len(av)
	if len(bv) > n {
		n = len(bv)
	}
	for i := 0; i < n; i++ {
		left, right := 0, 0
		if i < len(av) {
			left = av[i]
		}
		if i < len(bv) {
			right = bv[i]
		}
		if left != right {
			return left - right
		}
	}
	return 0
}

func versionParts(name string) []int {
	name = strings.TrimPrefix(strings.TrimSpace(name), "v")
	raw := strings.Split(name, ".")
	parts := make([]int, 0, len(raw))
	for _, part := range raw {
		if part == "" || !digitsOnly(part) {
			break
		}
		n := 0
		for _, r := range part {
			n = n*10 + int(r-'0')
		}
		parts = append(parts, n)
	}
	return parts
}
