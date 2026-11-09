package agentcontrol

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

const (
	agentResultPreviewHeadBytes = 3000
	agentResultPreviewTailBytes = 1000
)

type AgentResultReference struct {
	Preview   string
	Path      string
	Bytes     int
	Truncated bool
}

func (c *AgentControl) AgentResultReference(snap subagent.SubAgentSnapshot) AgentResultReference {
	raw := snap.Result
	if strings.TrimSpace(raw) == "" {
		return AgentResultReference{}
	}
	path := c.recordAgentResultArtifact(snap)
	preview, truncated := previewAgentResult(raw)
	return AgentResultReference{
		Preview:   preview,
		Path:      c.sessionArtifactRef(path),
		Bytes:     len([]byte(raw)),
		Truncated: truncated,
	}
}

func previewAgentResult(raw string) (string, bool) {
	if len(raw) <= agentResultPreviewHeadBytes+agentResultPreviewTailBytes {
		return raw, false
	}
	return stringutil.HeadTail(
		raw,
		agentResultPreviewHeadBytes,
		agentResultPreviewTailBytes,
		"\n\n[agent result preview truncated; read result_path for the full output]\n\n",
	), true
}

func AgentResultPreview(raw string) (string, bool) {
	return previewAgentResult(raw)
}

func (c *AgentControl) recordAgentResultArtifact(snap subagent.SubAgentSnapshot) string {
	if c == nil || c.harnessStore == nil || strings.TrimSpace(c.harnessDir) == "" || strings.TrimSpace(snap.ID) == "" || strings.TrimSpace(snap.Result) == "" {
		return ""
	}
	path := filepath.Join(c.harnessDir, "artifacts", snap.ID, "result.md")
	id := snap.ID + "-result"
	if c.artifactAlreadyRecorded(id, path) {
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), securefs.DirMode); err != nil {
		return ""
	}
	if err := os.WriteFile(path, []byte(snap.Result), securefs.FileMode); err != nil {
		return ""
	}
	createdAt := snap.CompletedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_ = c.harnessStore.AddArtifact(harness.Artifact{
		ID:        id,
		TaskID:    snap.ID,
		RunID:     harnessRunID(snap.ID),
		Kind:      harness.ArtifactResult,
		Path:      path,
		Summary:   "full agent result",
		CreatedAt: createdAt,
	})
	return path
}

func (c *AgentControl) artifactAlreadyRecorded(id, path string) bool {
	if c == nil || c.harnessStore == nil || strings.TrimSpace(id) == "" {
		return false
	}
	artifacts, err := c.harnessStore.ListArtifacts()
	if err != nil {
		return false
	}
	for _, artifact := range artifacts {
		if artifact.ID != id {
			continue
		}
		if strings.TrimSpace(path) == "" {
			return true
		}
		if artifact.Path == path {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
		return false
	}
	return false
}

func (c *AgentControl) sessionArtifactRef(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || c == nil || strings.TrimSpace(c.harnessDir) == "" {
		return path
	}
	sessionDir := filepath.Dir(c.harnessDir)
	rel, err := filepath.Rel(sessionDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	if rel == "." {
		return "$SESSION_DIR"
	}
	return "$SESSION_DIR/" + filepath.ToSlash(rel)
}

func (c *AgentControl) sessionArtifactRefs(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if ref := c.sessionArtifactRef(path); strings.TrimSpace(ref) != "" && !stringSliceContains(out, ref) {
			out = append(out, ref)
		}
	}
	return out
}
