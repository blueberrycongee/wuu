package processsandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CanonicalPath resolves every existing symlink, including a dangling symlink's
// target, while preserving not-yet-created descendants. Missing output files
// must not hide an existing symlink into a protected directory.
func CanonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for links := 0; links < 255; links++ {
		volume := filepath.VolumeName(absolute)
		current := volume + string(filepath.Separator)
		parts := strings.Split(strings.TrimPrefix(absolute, current), string(filepath.Separator))
		restarted := false
		for index, part := range parts {
			if part == "" {
				continue
			}
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				return filepath.Join(append([]string{current}, parts[index+1:]...)...), nil
			}
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(current)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			absolute = filepath.Join(append([]string{target}, parts[index+1:]...)...)
			restarted = true
			break
		}
		if !restarted {
			return current, nil
		}
	}
	return "", fmt.Errorf("too many symlinks resolving protected filesystem path %q", path)
}
