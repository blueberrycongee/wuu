package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Match complete lines before applying any display budget. Scanner's default
// token limit made an unrelated generated line abort the entire search.
func scanFallbackLines(ctx context.Context, path string, visit func(int, []byte) bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for number := 1; len(data) > 0; number++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, rest, _ := bytes.Cut(data, []byte{'\n'})
		if !visit(number, bytes.TrimSuffix(line, []byte{'\r'})) {
			break
		}
		data = rest
	}
	return nil
}

func walkFallbackSearchFiles(ctx context.Context, root, searchRoot, include string, visit func(string, string) error) error {
	var paths []string
	err := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() {
			if isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if include == "" || matchGlob(include, rel) {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Explicit files, like ripgrep's positional files, bypass ignore rules.
	ignored := map[string]bool{}
	if info, err := os.Stat(searchRoot); err == nil && info.IsDir() && len(paths) > 0 {
		absolutePaths := make([]string, 0, len(paths))
		for _, rel := range paths {
			absolutePaths = append(absolutePaths, filepath.Join(root, filepath.FromSlash(rel)))
		}
		ignored, err = fallbackGitIgnoredPaths(ctx, searchRoot, absolutePaths)
		if err != nil {
			return err
		}
	}
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		if ignored[path] || isBinaryFile(path) {
			continue
		}
		if err := visit(path, rel); err != nil {
			if errors.Is(err, filepath.SkipAll) {
				return nil
			}
			return err
		}
	}
	return nil
}

// Let Git evaluate nested rules, negations, repository excludes, and worktrees
// rather than maintaining a second partial gitignore parser. Batch paths over
// stdin so large trees do not exceed argv limits. No file contents are read here.
func fallbackGitIgnoredPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	probe := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	probe.Dir = root
	out, err := probe.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return ignored, nil // Plain directories and installations without Git.
	}
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err = cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("check search ignore rules: %w: %s", err, stderr.String())
		}
	}
	for _, path := range strings.Split(string(out), "\x00") {
		if path != "" {
			ignored[path] = true
		}
	}
	return ignored, nil
}
