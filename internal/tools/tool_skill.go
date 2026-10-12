package tools

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
)

type LoadSkillTool struct{ env *Env }

func NewLoadSkillTool(env *Env) *LoadSkillTool { return &LoadSkillTool{env: env} }

func (t *LoadSkillTool) Name() string            { return "load_skill" }
func (t *LoadSkillTool) IsReadOnly() bool        { return true }
func (t *LoadSkillTool) IsConcurrencySafe() bool { return true }

func (t *LoadSkillTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "load_skill",
		Description: strings.Join([]string{
			"Load full instructions for a listed skill when the task matches it.",
			"Available skill names and summaries are in the system prompt; output returns a <skill_content name=\"...\"> block.",
		}, "\n"),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name (e.g. \"commit\" or \"review-pr\"). Leading slash is optional.",
				},
				"arguments": map[string]any{
					"type":        "string",
					"description": "Optional arguments string substituted into ${ARGUMENTS} placeholders in the skill body.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *LoadSkillTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var args struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Name) == "" {
		return "", errors.New("load_skill requires name")
	}
	skill, ok := t.env.FindSkill(args.Name)
	if !ok {
		return "", fmt.Errorf("skill %q not found. available: %s", args.Name, strings.Join(t.env.SkillNames(), ", "))
	}

	body := t.env.ProcessSkillBody(ctx, skill, args.Arguments)
	output, err := SkillContentBlock(ctx, skill, body)
	if err != nil {
		return "", err
	}

	result := map[string]any{
		"action": "load_skill",
		"title":  fmt.Sprintf("Loaded skill: %s", skill.Name),
		"output": output,
		"metadata": map[string]any{
			"name":   skill.Name,
			"dir":    skill.Dir,
			"source": skill.Source,
		},
	}
	return mustJSON(result)
}

// SkillContentBlock formats loaded instructions and bounded resource hints.
// Cancellation aborts loading; unreadable or oversized directories only omit hints.
func SkillContentBlock(ctx context.Context, skill skills.Skill, body string) (string, error) {
	dir := strings.TrimSpace(skill.Dir)
	files, incomplete, err := sampleSkillFiles(ctx, dir, 10)
	if err != nil {
		return "", err
	}
	base := skillBaseReference(dir)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	note := "Note: file list is sampled."
	if incomplete {
		note += " Sampling limits or unreadable directories may omit resources; use the base directory to locate files as needed."
	}
	return strings.Join([]string{
		fmt.Sprintf(`<skill_content name="%s">`, html.EscapeString(skill.Name)),
		fmt.Sprintf("# Skill: %s", skill.Name),
		"",
		strings.TrimSpace(body),
		"",
		"Base directory for this skill: " + base,
		"Relative paths in this skill (e.g., scripts/, reference/) are relative to this base directory.",
		note,
		"",
		"<skill_files>",
		files,
		"</skill_files>",
		"</skill_content>",
	}, "\n"), nil
}

func skillBaseReference(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "unknown"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
			return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
		}
	}
	return dir
}

// Resource hints must not turn a skill load into an unbounded directory scan.
const (
	skillSampleEntryBudget    = 1024
	skillSampleDirectoryLimit = 256
	skillSampleDepthLimit     = 16
)

func sampleSkillFiles(ctx context.Context, dir string, limit int) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" || limit <= 0 {
		return "", false, nil
	}
	// Keep the final dot: the OS must resolve dir as a directory before
	// opening it, even if a concurrent replacement turns it into a FIFO.
	root, err := os.OpenRoot(dir + string(filepath.Separator) + ".")
	if err != nil {
		return "", true, ctx.Err()
	}
	defer root.Close()
	remaining := skillSampleEntryBudget
	incomplete := false
	var files []string
	var walk func(string, int) error
	walk = func(relative string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if remaining <= 1 || depth > skillSampleDepthLimit || len(files) >= limit {
			incomplete = true
			return nil
		}
		readLimit := min(skillSampleDirectoryLimit, remaining-1)
		entries, err := readSkillSampleDirectory(root, relative, readLimit+1)
		remaining -= len(entries)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if (err != nil && !errors.Is(err, io.EOF)) || len(entries) > readLimit {
			// An incomplete readdir prefix is filesystem-order dependent. Omit
			// the whole directory rather than claim a deterministic sample.
			incomplete = true
			return nil
		}
		// Include the directory separator in ordering so depth-first traversal
		// matches sorting full paths, including names such as a.txt and a/.
		key := func(entry os.DirEntry) string {
			name := entry.Name()
			if entry.IsDir() {
				name += string(filepath.Separator)
			}
			return name
		}
		sort.Slice(entries, func(i, j int) bool { return key(entries[i]) < key(entries[j]) })
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if len(files) >= limit {
				incomplete = true
				break
			}
			path := filepath.Join(relative, entry.Name())
			if entry.IsDir() {
				if err := walk(path, depth+1); err != nil {
					return err
				}
			} else if !strings.EqualFold(entry.Name(), "SKILL.md") {
				// Symlink names may appear, but their targets are never enumerated.
				files = append(files, filepath.Join(dir, path))
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return "", false, err
	}
	for i, file := range files {
		files[i] = fmt.Sprintf("<file>%s</file>", html.EscapeString(file))
	}
	return strings.Join(files, "\n"), incomplete, ctx.Err()
}

func readSkillSampleDirectory(root *os.Root, relative string, limit int) ([]os.DirEntry, error) {
	// A final dot makes every resource path component a directory lookup.
	// Do not use filepath.Join: cleaning away the dot could block on a FIFO.
	// Root keeps raced symlink resolution inside the selected skill directory.
	file, err := root.Open(relative + string(filepath.Separator) + ".")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return file.ReadDir(limit)
}
