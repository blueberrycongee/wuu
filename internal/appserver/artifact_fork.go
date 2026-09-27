package appserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// preserveForkArtifacts gives the fork independent ownership of every managed
// artifact retained in its copied history. The source thread can then be
// deleted without breaking the fork's previews.
func preserveForkArtifacts(stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage) error {
	if err := preserveForkInputImages(stateDir, sourceThreadID, forkThreadID, history); err != nil {
		return err
	}
	artifactIDs := make(map[string]struct{})
	for index := range history {
		result := history[index].ToolResult
		if result == nil {
			continue
		}
		for partIndex := range result.Content {
			uri, artifactID, ok := forkArtifactURI(result.Content[partIndex].URI, sourceThreadID, forkThreadID)
			if !ok {
				continue
			}
			artifactIDs[artifactID] = struct{}{}
			result.Content[partIndex].URI = uri
		}
	}
	if len(artifactIDs) == 0 {
		return nil
	}
	sourceRoot := filepath.Join(statepath.SessionArtifactDir(stateDir, sourceThreadID), "artifacts")
	destinationRoot := filepath.Join(statepath.SessionArtifactDir(stateDir, forkThreadID), "artifacts")
	for artifactID := range artifactIDs {
		destination := filepath.Join(destinationRoot, artifactID)
		if err := copyForkArtifactDirectory(filepath.Join(sourceRoot, artifactID), destination); err != nil {
			return fmt.Errorf("copy artifact %s into fork: %w", artifactID, err)
		}
		if err := rewriteForkArtifactManifest(destination, sourceThreadID, forkThreadID); err != nil {
			return fmt.Errorf("rewrite artifact %s ownership: %w", artifactID, err)
		}
	}
	return nil
}

func rewriteForkArtifactManifest(artifactDir, sourceThreadID, forkThreadID string) error {
	path := filepath.Join(artifactDir, ".artifact.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	manifest["thread_id"] = forkThreadID
	manifest["forked_from_thread_id"] = sourceThreadID
	updated, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0o600)
}

func forkArtifactURI(raw, sourceThreadID, forkThreadID string) (string, string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "wuu-artifact" {
		return "", "", false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 3 || segments[0] != sourceThreadID || !validManagedArtifactID(segments[1]) {
		return "", "", false
	}
	artifactID := segments[1]
	segments[0] = forkThreadID
	parsed.Path = "/" + strings.Join(segments, "/")
	return parsed.String(), artifactID, true
}

func validManagedArtifactID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func copyForkArtifactDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("managed artifact contains a non-regular file")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
}
