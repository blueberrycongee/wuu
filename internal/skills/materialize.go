package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// systemSkillsMarker holds the content fingerprint of the last materialization,
// so re-extraction is skipped when the embedded bytes are unchanged.
const systemSkillsMarker = ".fingerprint"

// systemSkillsDir is the on-disk location, under <cacheRoot>/skills, where the
// binary-embedded bundled skills are extracted. The leading dot keeps it out of
// the sibling skill scan of <cacheRoot>/skills and mirrors Claude Code / codex,
// which materialize built-in skills under a hidden ".system" root.
func systemSkillsDir(cacheRoot string) string {
	return filepath.Join(cacheRoot, "skills", ".system")
}

// MaterializeBundled extracts the embedded bundled/ skill tree onto disk under
// <cacheRoot>/skills/.system and returns that directory. Supporting files
// (references/, scripts/, assets/) are written alongside each SKILL.md so the
// model's Read/Bash tools can reach them by real path.
//
// It is idempotent and cheap: a content fingerprint marker guards re-extraction,
// so it only rewrites when the embedded bytes change (e.g. after a binary
// upgrade). Returns "" with a nil error when cacheRoot is empty, letting callers
// fall back to embed-only parsing.
func MaterializeBundled(cacheRoot string) (string, error) {
	if strings.TrimSpace(cacheRoot) == "" {
		return "", nil
	}
	dest := systemSkillsDir(cacheRoot)
	want := bundledFingerprint()
	if got, err := os.ReadFile(filepath.Join(dest, systemSkillsMarker)); err == nil && string(got) == want {
		return dest, nil
	}
	// Rewrite from scratch so files removed from the binary never linger on disk.
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := writeEmbeddedTree(dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dest, systemSkillsMarker), []byte(want), 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// writeEmbeddedTree copies every embedded file under bundled/ to dest, stripping
// the leading "bundled/" segment so skills land at dest/<name>/SKILL.md.
func writeEmbeddedTree(dest string) error {
	return fs.WalkDir(bundledFS, "bundled", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "bundled"), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bundledFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// bundledFingerprint hashes every embedded bundled path and its contents so the
// marker changes whenever the shipped skills change.
func bundledFingerprint() string {
	var paths []string
	_ = fs.WalkDir(bundledFS, "bundled", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		data, err := bundledFS.ReadFile(p)
		if err != nil {
			continue
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
