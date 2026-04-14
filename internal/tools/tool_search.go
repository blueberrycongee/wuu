package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ---------------------------------------------------------------------------
// grep
// ---------------------------------------------------------------------------

type GrepTool struct{ env *Env }

func NewGrepTool(env *Env) *GrepTool { return &GrepTool{env: env} }

func (t *GrepTool) Name() string            { return "grep" }
func (t *GrepTool) IsReadOnly() bool         { return true }
func (t *GrepTool) IsConcurrencySafe() bool  { return true }

func (t *GrepTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "grep",
		Description: "Search file contents using a regex pattern. Returns matching lines with file paths and line numbers.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in. Default is workspace root.",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "Glob pattern to filter files (e.g. '*.go', '*.ts').",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GrepTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errors.New("grep requires pattern")
	}

	if _, err := regexp.Compile(args.Pattern); err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	searchRoot := t.env.RootDir
	if strings.TrimSpace(args.Path) != "" {
		resolved, err := t.env.ResolvePath(args.Path)
		if err != nil {
			return "", err
		}
		searchRoot = resolved
	}

	const limit = 250
	matches, err := grepWithRipgrep(t.env.RootDir, args.Pattern, searchRoot, args.Include, limit)
	if err != nil {
		matches, err = grepWithFallback(t.env.RootDir, args.Pattern, searchRoot, args.Include, limit)
		if err != nil {
			return "", err
		}
	}

	result := map[string]any{
		"pattern":   args.Pattern,
		"total":     len(matches),
		"truncated": len(matches) >= limit,
		"matches":   matches,
	}
	out, err := mustJSON(result)
	if err != nil {
		return "", err
	}
	if len(out) > maxGrepOutputBytes {
		out = out[:maxGrepOutputBytes]
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// glob
// ---------------------------------------------------------------------------

type GlobTool struct{ env *Env }

func NewGlobTool(env *Env) *GlobTool { return &GlobTool{env: env} }

func (t *GlobTool) Name() string            { return "glob" }
func (t *GlobTool) IsReadOnly() bool         { return true }
func (t *GlobTool) IsConcurrencySafe() bool  { return true }

func (t *GlobTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "glob",
		Description: "Find files matching a glob pattern in the workspace. Supports ** for recursive matching.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts', '*.json').",
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", errors.New("glob requires pattern")
	}

	const limit = 500
	matches, err := globWithRipgrep(t.env.RootDir, args.Pattern, limit)
	if err != nil {
		matches, err = globWithFallback(t.env.RootDir, args.Pattern, limit)
		if err != nil {
			return "", err
		}
	}

	result := map[string]any{
		"pattern":   args.Pattern,
		"total":     len(matches),
		"truncated": len(matches) >= limit,
		"files":     matches,
	}
	return mustJSON(result)
}

// ---------------------------------------------------------------------------
// Shared grep/glob implementation (extracted from old Toolkit methods)
// ---------------------------------------------------------------------------

func grepWithRipgrep(rootDir, pattern, searchRoot, include string, limit int) ([]grepMatch, error) {
	relSearchRoot, err := filepath.Rel(rootDir, searchRoot)
	if err != nil {
		return nil, err
	}
	if relSearchRoot == "." {
		relSearchRoot = ""
	}
	cmd := buildRGGrepCommand(context.Background(), pattern, relSearchRoot, include)
	if cmd == nil {
		return nil, errors.New("ripgrep not available")
	}
	cmd.Dir = rootDir

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []grepMatch{}, nil
		}
		return nil, err
	}

	matches := make([]grepMatch, 0, min(limit, 16))
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event rgJSONEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("parse ripgrep output: %w", err)
		}
		if event.Type != "match" {
			continue
		}
		matchPath := event.Data.Path.Text
		if !filepath.IsAbs(matchPath) {
			matchPath = filepath.Join(rootDir, matchPath)
		}
		rel, err := filepath.Rel(rootDir, matchPath)
		if err != nil {
			continue
		}
		matches = append(matches, grepMatch{
			File:    filepath.ToSlash(rel),
			Line:    event.Data.LineNumber,
			Content: strings.TrimRight(event.Data.Lines.Text, "\r\n"),
		})
		if len(matches) >= limit {
			break
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File == matches[j].File {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].File < matches[j].File
	})
	return matches, nil
}

func grepWithFallback(rootDir, pattern, searchRoot, include string, limit int) ([]grepMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	matches := make([]grepMatch, 0, min(limit, 16))
	walkErr := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if include != "" && !matchGlob(include, rel) {
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					File:    rel,
					Line:    lineNum,
					Content: line,
				})
				if len(matches) >= limit {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan %s: %w", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].File == matches[j].File {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].File < matches[j].File
	})
	return matches, nil
}

func globWithRipgrep(rootDir, pattern string, limit int) ([]string, error) {
	cmd := buildRGFilesCommand(context.Background(), pattern)
	if cmd == nil {
		return nil, errors.New("ripgrep not available")
	}
	cmd.Dir = rootDir

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		return nil, err
	}

	matches := make([]string, 0, min(limit, 16))
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		matches = append(matches, filepath.ToSlash(string(entry)))
		if len(matches) >= limit {
			break
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func globWithFallback(rootDir, pattern string, limit int) ([]string, error) {
	matches := make([]string, 0, min(limit, 16))
	_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(pattern, rel) {
			matches = append(matches, rel)
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(matches)
	return matches, nil
}
