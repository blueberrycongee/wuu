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

// Reuse plugin artifact storage and its URI/manifest contract without requiring
// a plugin invocation or granting a model control over thread/storage identity.
func newArtifactPublisher(wuuHome string) tools.ArtifactPublisher {
	return func(ctx context.Context, request tools.ArtifactPublishRequest) (toolresult.ContentPart, error) {
		if request.ThreadID == "" || request.ThreadID == "." || request.ThreadID == ".." || strings.ContainsAny(request.ThreadID, `/\`) {
			return toolresult.ContentPart{}, fmt.Errorf("invalid artifact thread id")
		}
		if err := ctx.Err(); err != nil {
			return toolresult.ContentPart{}, err
		}
		// Stat before opening so directories and special files (e.g. FIFOs) never
		// block a tool call. importArtifact also checks the opened descriptor.
		info, err := os.Stat(request.Path)
		if err != nil {
			return toolresult.ContentPart{}, fmt.Errorf("stat artifact: %w", err)
		}
		if !info.Mode().IsRegular() {
			return toolresult.ContentPart{}, fmt.Errorf("artifact path must name a regular file")
		}
		if info.Size() > maxImportedArtifactBytes {
			return toolresult.ContentPart{}, fmt.Errorf("artifact exceeds %d bytes", maxImportedArtifactBytes)
		}
		file, err := os.Open(request.Path)
		if err != nil {
			return toolresult.ContentPart{}, err
		}
		defer file.Close()
		mimeType, err := presentedArtifactMIME(file, request.Path)
		if err != nil {
			return toolresult.ContentPart{}, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return toolresult.ContentPart{}, err
		}
		artifact, err := importArtifact(ctx, wuuHome, request.StateDir, pluginhost.ToolExecutionScope{
			ExecutionSnapshot: pluginhost.ExecutionSnapshot{
				ThreadID: request.ThreadID, CallID: request.CallID, CWD: request.CWD,
				ID: request.CallID, PluginID: "builtin.present_artifact",
			},
		}, pluginhost.ArtifactImportParams{Path: request.Path, MIMEType: mimeType}, file)
		if err != nil {
			return toolresult.ContentPart{}, err
		}
		part := toolresult.ContentPart{Type: toolresult.ContentTypeFile, Artifact: &toolresult.ArtifactPresentation{Placement: toolresult.ArtifactPlacementTurnEnd}}
		if strings.HasPrefix(mimeType, "image/") {
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
