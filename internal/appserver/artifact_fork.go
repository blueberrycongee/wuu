package appserver

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Remove this compatibility entry after the two session-fork callers retain
// the selected provider through Server.preserveForkArtifacts. It never copies
// managed artifacts through the old filesystem path.
func preserveForkArtifacts(stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage) error {
	for _, message := range history {
		if message.ToolResult == nil {
			continue
		}
		for _, part := range message.ToolResult.Content {
			if part.Artifact != nil {
				return errors.New("artifact fork requires the selected provider")
			}
		}
	}
	return preserveForkInputImages(stateDir, sourceThreadID, forkThreadID, history)
}

// preserveForkArtifacts routes all managed artifact identities through the
// captured provider, so external URI schemes never require filesystem parsing.
func (s *Server) preserveForkArtifacts(stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage) error {
	if err := preserveForkInputImages(stateDir, sourceThreadID, forkThreadID, history); err != nil {
		return err
	}
	var services *runtime.StateServices
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	replacements := make(map[string]pluginhost.ArtifactImportResult)
	for index := range history {
		result := history[index].ToolResult
		if result == nil {
			continue
		}
		for partIndex := range result.Content {
			part := &result.Content[partIndex]
			if part.Artifact == nil {
				continue
			}
			if services == nil {
				var err error
				services, release, err = s.rt.RetainStateServices()
				if err != nil {
					return err
				}
			}
			originalURI := part.URI
			artifact, ok := replacements[originalURI]
			if !ok {
				_, managed, err := services.Artifacts.Resolve(context.Background(), sourceThreadID, originalURI)
				if err != nil {
					return err
				}
				if !managed {
					return errors.New("history artifact is unavailable from the selected provider")
				}
				artifact, err = services.Artifacts.Fork(context.Background(), sourceThreadID, forkThreadID, originalURI)
				if err != nil {
					return err
				}
				replacements[originalURI] = artifact
			}
			part.URI, part.Name, part.MIMEType = artifact.URI, artifact.Name, artifact.MIMEType
			part.Artifact.Ref, part.Artifact.SHA256, part.Artifact.SizeBytes = artifact.ID, artifact.SHA256, &artifact.Size
		}
	}
	return nil
}
