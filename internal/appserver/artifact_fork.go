package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
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
func (s *Server) preserveForkArtifacts(services *runtime.StateServices, stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage) error {
	return s.preserveForkArtifactsWithDisplay(services, stateDir, sourceThreadID, forkThreadID, history, nil)
}

func (s *Server) preserveForkArtifactsWithDisplay(services *runtime.StateServices, stateDir, sourceThreadID, forkThreadID string, history []providers.ChatMessage, display []forkDisplayRecord) error {
	if err := preserveForkInputImages(stateDir, sourceThreadID, forkThreadID, history); err != nil {
		return err
	}
	replacements := make(map[string]pluginhost.ArtifactImportResult)
	copyResult := func(result *toolresult.Result) error {
		if result == nil {
			return nil
		}
		for partIndex := range result.Content {
			part := &result.Content[partIndex]
			if part.Artifact == nil {
				continue
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
		return nil
	}
	for index := range history {
		if err := copyResult(history[index].ToolResult); err != nil {
			return err
		}
	}
	for index := range display {
		display[index].record.ToolResult = cloneToolResult(display[index].record.ToolResult)
		if err := copyResult(display[index].record.ToolResult); err != nil {
			return err
		}
	}
	return nil
}
