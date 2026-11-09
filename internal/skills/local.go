package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Local discovery is shallow, like DiscoverSourceDirs. These budgets bound one
// complete refresh, not a partial catalog that could change source precedence.
const (
	localEntryLimit = 8192
	localFileLimit  = 1024 * 1024
	localByteLimit  = 16 * 1024 * 1024
)

var errLocalReadBudget = errors.New("local skill discovery exceeds 16 MiB")

type localFile struct {
	resolved  string
	command   bool
	directory bool
}

type localScan struct {
	ctx     context.Context
	entries int
	bytes   int
}

// DiscoverLocal reads mutable skills and command templates with one shared work
// budget. Missing roots are normal; an incomplete enumeration fails the refresh.
// Plugin packages and bundled skills use their separate, pinned discovery path.
func DiscoverLocal(ctx context.Context, skillDirs, commandDirs []SourceDir) ([]Skill, []Skill, error) {
	scan := &localScan{ctx: ctx}
	if len(skillDirs)+len(commandDirs) > 512 {
		return nil, nil, errors.New("local skill discovery exceeds 512 roots")
	}
	read := func(dirs []SourceDir, commands bool) ([]Skill, error) {
		var out []Skill
		for _, root := range dirs {
			entries, err := scan.directory(root.Path)
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				path := filepath.Join(root.Path, entry.Name())
				directory := entry.IsDir()
				if directory {
					if commands || !isPortableSkillName(entry.Name()) {
						continue
					}
					children, err := scan.directory(path)
					if err != nil {
						return nil, err
					}
					path = ""
					for _, child := range children {
						if !child.IsDir() && strings.EqualFold(child.Name(), "SKILL.md") {
							path = filepath.Join(root.Path, entry.Name(), child.Name())
							break
						}
					}
					if path == "" {
						continue
					}
				} else if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
					continue
				}
				file := &localFile{command: commands, directory: directory}
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					continue
				}
				file.resolved = resolved
				skill, count, err := readLocal(scan.ctx, Skill{Path: path, Source: root.Source, local: file}, min(localFileLimit, localByteLimit-scan.bytes))
				scan.bytes += count
				if errors.Is(err, errLocalReadBudget) || scan.bytes > localByteLimit {
					return nil, errLocalReadBudget
				}
				if err := scan.ctx.Err(); err != nil {
					return nil, err
				}
				if err != nil {
					continue
				}
				out = append(out, skill)
			}
		}
		return out, nil
	}
	skills, err := read(skillDirs, false)
	if err != nil {
		return nil, nil, err
	}
	commands, err := read(commandDirs, true)
	return skills, commands, err
}

func (s *localScan) directory(path string) ([]os.DirEntry, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	// The trailing separator and dot require a directory even during a rename
	// race, instead of opening a FIFO substituted for the directory.
	root, err := os.OpenRoot(filepath.Clean(path) + string(os.PathSeparator) + ".")
	// Unreadable roots are skipped like missing ones, matching earlier discovery.
	// One locked directory must not hide every other skill, including bundled ones.
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, os.ErrPermission) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(localEntryLimit - s.entries + 1)
	s.entries += len(entries)
	if s.entries > localEntryLimit {
		return nil, errors.New("local skill discovery exceeds 8192 directory entries")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// ReloadLocal reads only the already admitted file identity. It never resolves a
// name against another source. Plugin/bundled content remains generation-pinned.
func ReloadLocal(ctx context.Context, admitted Skill) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if admitted.local == nil {
		return admitted, nil
	}
	fresh, _, err := readLocal(ctx, admitted, localFileLimit)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q is no longer readable: %w", admitted.Name, err)
	}
	if fresh.Name != admitted.Name {
		return Skill{}, fmt.Errorf("skill %q changed identity; select it again", admitted.Name)
	}
	return fresh, nil
}

func readLocal(ctx context.Context, admitted Skill, readLimit int) (Skill, int, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, 0, err
	}
	resolved, err := filepath.EvalSymlinks(admitted.Path)
	if err != nil {
		return Skill{}, 0, err
	}
	if resolved != admitted.local.resolved {
		return Skill{}, 0, errors.New("skill file target changed")
	}
	root, err := os.OpenRoot(filepath.Dir(resolved) + string(os.PathSeparator) + ".")
	if err != nil {
		return Skill{}, 0, err
	}
	defer root.Close()
	// O_NONBLOCK prevents a raced FIFO replacement from blocking before Stat.
	file, err := root.OpenFile(filepath.Base(resolved), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Skill{}, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Skill{}, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > localFileLimit {
		return Skill{}, 0, errors.New("skill must be a regular file of at most 1 MiB")
	}
	if info.Size() > int64(readLimit) {
		return Skill{}, 0, errLocalReadBudget
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(readLimit)+1))
	if err != nil {
		return Skill{}, len(data), err
	}
	if err := ctx.Err(); err != nil {
		return Skill{}, len(data), err
	}
	if len(data) > readLimit && readLimit < localFileLimit {
		return Skill{}, len(data), errLocalReadBudget
	}
	if len(data) > localFileLimit {
		return Skill{}, len(data), errors.New("skill exceeds 1 MiB")
	}
	current, err := os.Stat(admitted.Path)
	if err != nil {
		return Skill{}, len(data), err
	}
	after, err := file.Stat()
	if err != nil {
		return Skill{}, len(data), err
	}
	target, err := filepath.EvalSymlinks(admitted.Path)
	if err != nil {
		return Skill{}, len(data), err
	}
	if target != resolved || !os.SameFile(info, current) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return Skill{}, len(data), errors.New("skill changed while being read; retry")
	}
	if !utf8.Valid(data) {
		return Skill{}, len(data), errors.New("skill is not valid UTF-8")
	}
	content := string(data)
	var skill Skill
	if admitted.local.command && !strings.HasPrefix(strings.TrimSpace(content), "---") {
		skill = Skill{Content: normalizeCommandArguments(content), UserInvocable: true}
	} else {
		// Do not publish half-written frontmatter with default invocation policy.
		lines := strings.Split(content, "\n")
		closed := false
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "---" {
				closed = true
				break
			}
		}
		if !closed {
			return Skill{}, len(data), errors.New("incomplete skill frontmatter")
		}
		front, _, err := splitFrontmatter(content)
		if err != nil {
			return Skill{}, len(data), err
		}
		var metadata map[string]any
		if err := yaml.Unmarshal([]byte(front), &metadata); err != nil {
			return Skill{}, len(data), err
		}
		seen := make(map[string]bool, len(metadata))
		for key, value := range metadata {
			normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "_", "-"))
			if seen[normalized] {
				return Skill{}, len(data), fmt.Errorf("duplicate skill metadata %q", normalized)
			}
			seen[normalized] = true
			if admitted.local.command || normalized != "user-invocable" && normalized != "disable-model-invocation" {
				continue
			}
			switch v := value.(type) {
			case bool:
			case string:
				switch strings.ToLower(strings.TrimSpace(v)) {
				case "true", "false", "yes", "no", "1", "0", "on", "off":
				default:
					return Skill{}, len(data), fmt.Errorf("invalid %s value", key)
				}
			default:
				return Skill{}, len(data), fmt.Errorf("invalid %s value", key)
			}
		}
		skill, err = parseSkill(content, admitted.Source)
		if err != nil {
			return Skill{}, len(data), err
		}
		if admitted.local.command {
			skill = Skill{Description: skill.Description, ArgumentHint: skill.ArgumentHint, Content: normalizeCommandArguments(skill.Content), UserInvocable: true}
		}
	}
	skill.Path, skill.Dir, skill.Source, skill.local = admitted.Path, filepath.Dir(admitted.Path), admitted.Source, admitted.local
	if admitted.local.directory {
		skill.Name = filepath.Base(skill.Dir)
	} else if admitted.local.command || skill.Name == "" {
		skill.Name = commandName(admitted.Path)
	}
	skill.Name = canonicalName(skill.Name)
	if err := ctx.Err(); err != nil {
		return Skill{}, len(data), err
	}
	return skill, len(data), nil
}
