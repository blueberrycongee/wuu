package runtime

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// NewArtifactPublisher routes built-in presentation through the selected
// artifact provider, including media inspection and immutable snapshot creation.
func NewArtifactPublisher(client *ArtifactClient) tools.ArtifactPublisher {
	return func(ctx context.Context, request tools.ArtifactPublishRequest) (toolresult.ContentPart, error) {
		if !validArtifactThreadID(request.ThreadID) {
			return toolresult.ContentPart{}, fmt.Errorf("invalid artifact thread id")
		}
		artifact, err := client.Import(ctx, ArtifactScope{ThreadID: request.ThreadID, ExecutionID: request.CallID, CallID: request.CallID, CWD: request.CWD, PluginID: "builtin.present_artifact"}, pluginhost.ArtifactImportParams{Path: request.Path}, true)
		if err != nil {
			return toolresult.ContentPart{}, err
		}
		part := toolresult.ContentPart{Type: toolresult.ContentTypeFile, Artifact: &toolresult.ArtifactPresentation{Placement: toolresult.ArtifactPlacementTurnEnd}}
		if strings.HasPrefix(artifact.MIMEType, "image/") {
			part.Type = toolresult.ContentTypeImage
			part.Artifact.Placement = toolresult.ArtifactPlacementInline
		}
		applyManagedArtifact(&part, artifact)
		return part, nil
	}
}

func presentedArtifactMIME(file *os.File, name string) (string, error) {
	probe := make([]byte, 512)
	n, err := file.Read(probe)
	if err != nil && err != io.EOF {
		return "", err
	}
	detected := http.DetectContentType(probe[:n])
	ext := strings.ToLower(filepath.Ext(name))
	if ext == ".svg" {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		// XML is inspected as data only. The renderer uses an img, never raw SVG
		// markup. Limit prolog scanning so a bogus file cannot monopolize parsing.
		decoder := xml.NewDecoder(io.LimitReader(file, 64*1024))
		for {
			token, err := decoder.Token()
			if err != nil {
				return "", fmt.Errorf("artifact is not an SVG image: %w", err)
			}
			if root, ok := token.(xml.StartElement); ok {
				if root.Name.Local != "svg" || (root.Name.Space != "" && root.Name.Space != "http://www.w3.org/2000/svg") {
					return "", fmt.Errorf("artifact is not an SVG image")
				}
				return "image/svg+xml", nil
			}
		}
	}
	declared := mime.TypeByExtension(ext)
	// Presentation dispatch uses media types, not Content-Type parameters.
	// TypeByExtension adds charset to common text formats such as HTML.
	if mediaType, _, err := mime.ParseMediaType(declared); err == nil {
		declared = mediaType
	}
	if strings.HasPrefix(detected, "image/") {
		return detected, nil
	}
	if strings.HasPrefix(declared, "image/") {
		return "", fmt.Errorf("artifact has an image extension but no recognized image content; use PNG, JPEG, GIF, WebP or SVG")
	}
	if declared != "" {
		return declared, nil
	}
	mediaType, _, err := mime.ParseMediaType(detected)
	if err != nil {
		return "", err
	}
	return mediaType, nil
}
