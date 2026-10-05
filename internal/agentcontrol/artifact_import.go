package agentcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

func (c *AgentControl) importReportedArtifacts(taskID string, rawPaths []string) ([]string, error) {
	paths := trimStringSlice(rawPaths)
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path, err := c.importReportedArtifact(taskID, raw)
		if err != nil {
			return nil, err
		}
		if path != "" && !stringSliceContains(out, path) {
			out = append(out, path)
		}
	}
	return out, nil
}

func (c *AgentControl) importReportedArtifact(taskID, rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", nil
	}
	if c == nil || c.harnessStore == nil || strings.TrimSpace(c.harnessDir) == "" {
		return "", errorsForArtifact(rawPath, "harness store not configured")
	}

	source, managed, err := c.resolveReportedArtifactPath(taskID, rawPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errorsForArtifact(rawPath, "file does not exist")
		}
		return "", fmt.Errorf("agent_report artifact %q: stat failed: %w", rawPath, err)
	}
	if info.IsDir() {
		return "", errorsForArtifact(rawPath, "directories are not supported; report a file artifact")
	}

	path := source
	if !managed {
		path, err = c.copyReportedArtifact(taskID, rawPath, source)
		if err != nil {
			return "", err
		}
	}
	c.recordReportedArtifact(taskID, rawPath, path)
	return path, nil
}

func (c *AgentControl) resolveReportedArtifactPath(taskID, rawPath string) (path string, managed bool, err error) {
	if path, ok, err := c.resolveSessionArtifactRef(rawPath); ok || err != nil {
		return path, ok, err
	}
	if filepath.IsAbs(rawPath) {
		resolved, err := filepath.Abs(filepath.Clean(rawPath))
		if err != nil {
			return "", false, fmt.Errorf("agent_report artifact %q: resolve failed: %w", rawPath, err)
		}
		if c.pathInSessionDir(resolved) {
			return resolved, true, nil
		}
		root := c.reportArtifactWorkspaceRoot(taskID)
		if !pathWithinRoot(root, resolved) {
			return "", false, errorsForArtifact(rawPath, "absolute path must be inside the task workspace or current session artifacts")
		}
		return resolved, false, nil
	}
	root := c.reportArtifactWorkspaceRoot(taskID)
	resolved, err := filepath.Abs(filepath.Join(root, rawPath))
	if err != nil {
		return "", false, fmt.Errorf("agent_report artifact %q: resolve failed: %w", rawPath, err)
	}
	if !pathWithinRoot(root, resolved) {
		return "", false, errorsForArtifact(rawPath, "relative path escapes the task workspace")
	}
	return resolved, false, nil
}

func (c *AgentControl) resolveSessionArtifactRef(input string) (string, bool, error) {
	sessionDir := c.sessionDir()
	if sessionDir == "" {
		return "", false, nil
	}
	const prefix = "$SESSION_DIR"
	if input != prefix && !strings.HasPrefix(input, prefix+"/") && !strings.HasPrefix(input, prefix+string(filepath.Separator)) {
		return "", false, nil
	}
	suffix := strings.TrimPrefix(input, prefix)
	suffix = strings.TrimPrefix(suffix, "/")
	suffix = strings.TrimPrefix(suffix, string(filepath.Separator))
	resolved, err := filepath.Abs(filepath.Join(sessionDir, filepath.FromSlash(suffix)))
	if err != nil {
		return "", true, fmt.Errorf("agent_report artifact %q: resolve failed: %w", input, err)
	}
	if !pathWithinRoot(sessionDir, resolved) {
		return "", true, errorsForArtifact(input, "path escapes the current session artifact directory")
	}
	return resolved, true, nil
}

func (c *AgentControl) copyReportedArtifact(taskID, rawPath, source string) (string, error) {
	dir := filepath.Join(c.harnessDir, "artifacts", taskID, "reported")
	if err := os.MkdirAll(dir, securefs.DirMode); err != nil {
		return "", fmt.Errorf("agent_report artifact %q: create import dir: %w", rawPath, err)
	}
	dest := filepath.Join(dir, reportedArtifactFilename(rawPath, source))
	if err := copyFileAtomic(source, dest); err != nil {
		return "", fmt.Errorf("agent_report artifact %q: import failed: %w", rawPath, err)
	}
	return dest, nil
}

func (c *AgentControl) recordReportedArtifact(taskID, rawPath, path string) {
	if c == nil || c.harnessStore == nil || path == "" {
		return
	}
	if c.artifactPathAlreadyRecorded(taskID, path) {
		return
	}
	_ = c.harnessStore.AddArtifact(harness.Artifact{
		ID:        reportedArtifactID(taskID, rawPath),
		TaskID:    taskID,
		RunID:     harnessRunID(taskID),
		Kind:      harness.ArtifactEvidence,
		Path:      path,
		Summary:   "agent-reported artifact",
		CreatedAt: time.Now().UTC(),
	})
}

func (c *AgentControl) artifactPathAlreadyRecorded(taskID, path string) bool {
	artifacts, err := c.harnessStore.ListArtifacts()
	if err != nil {
		return false
	}
	for _, artifact := range artifacts {
		if artifact.TaskID == taskID && artifact.Path == path {
			return true
		}
	}
	return false
}

func (c *AgentControl) reportArtifactWorkspaceRoot(taskID string) string {
	if task, ok := c.harnessTask(taskID); ok && strings.TrimSpace(task.Workspace.Root) != "" {
		return task.Workspace.Root
	}
	return c.ParentRepo()
}

func (c *AgentControl) sessionDir() string {
	if c == nil || strings.TrimSpace(c.harnessDir) == "" {
		return ""
	}
	return filepath.Dir(c.harnessDir)
}

func (c *AgentControl) pathInSessionDir(path string) bool {
	sessionDir := c.sessionDir()
	return sessionDir != "" && pathWithinRoot(sessionDir, path)
}

func reportedArtifactFilename(rawPath, source string) string {
	ext := filepath.Ext(source)
	base := strings.TrimSuffix(filepath.Base(source), ext)
	name := sanitizeArtifactID(base)
	if len(name) > 64 {
		name = strings.Trim(name[:64], "_")
	}
	if name == "" {
		name = "artifact"
	}
	sum := sha256.Sum256([]byte(rawPath))
	return name + "-" + hex.EncodeToString(sum[:])[:8] + ext
}

func reportedArtifactID(taskID, rawPath string) string {
	slug := sanitizeArtifactID(rawPath)
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "_")
	}
	if slug == "" {
		slug = "artifact"
	}
	sum := sha256.Sum256([]byte(rawPath))
	return taskID + "-artifact-" + slug + "-" + hex.EncodeToString(sum[:])[:8]
}

func copyFileAtomic(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, copyErr := io.Copy(tmp, in)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpName)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return closeErr
	}
	if err := os.Rename(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func pathWithinRoot(root, path string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	evalRoot := absRoot
	if ev, err := filepath.EvalSymlinks(evalRoot); err == nil {
		evalRoot = ev
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	evalPath := absPath
	if ev, err := filepath.EvalSymlinks(evalPath); err == nil {
		evalPath = ev
	} else if rel, relErr := filepath.Rel(absRoot, absPath); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		evalPath = filepath.Join(evalRoot, rel)
	}
	rel, err := filepath.Rel(evalRoot, evalPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func errorsForArtifact(path, reason string) error {
	return fmt.Errorf("agent_report artifact %q: %s", strings.TrimSpace(path), reason)
}
