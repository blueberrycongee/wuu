package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type historyAttachment struct {
	metadata pluginhost.SessionHistoryAttachment
	data     string
	uri      string
}

func recordAttachments(record session.HistoryRecord) []historyAttachment {
	var result []historyAttachment
	add := func(kind string, index int, name, media, data string) {
		if len(data) > base64.StdEncoding.EncodedLen(MaxClientInputAttachmentBytes) {
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(decoded) == 0 {
			return
		}
		sum := sha256.Sum256(decoded)
		result = append(result, historyAttachment{metadata: pluginhost.SessionHistoryAttachment{Kind: kind, Index: index, Name: name, MIMEType: media, Size: int64(len(decoded)), SHA256: hex.EncodeToString(sum[:])}, data: data})
	}
	var images []providers.InputImage
	if json.Unmarshal(record.Images, &images) == nil {
		for i, image := range images {
			add("image", i, fmt.Sprintf("image-%d", i+1), image.MediaType, image.Data)
		}
	}
	var files []providers.InputFile
	if json.Unmarshal(record.Files, &files) == nil {
		for i, file := range files {
			add("file", i, file.Filename, file.MediaType, file.Data)
		}
	}
	var output toolresult.Result
	if json.Unmarshal(record.ToolResult, &output) == nil {
		for i, part := range output.Content {
			if part.Artifact != nil && part.Artifact.SHA256 != "" && part.Artifact.SizeBytes != nil && part.URI != "" {
				result = append(result, historyAttachment{metadata: pluginhost.SessionHistoryAttachment{Kind: "artifact", Index: i, Name: part.Name, MIMEType: part.MIMEType, Size: *part.Artifact.SizeBytes, SHA256: part.Artifact.SHA256}, uri: part.URI})
			} else if part.Data != "" {
				add("artifact", i, part.Name, part.MIMEType, part.Data)
			}
		}
	}
	return result
}

// HistoryAttachmentMetadata returns the same visible attachment references exposed by session history.
func HistoryAttachmentMetadata(record session.HistoryRecord) []pluginhost.SessionHistoryAttachment {
	var result []pluginhost.SessionHistoryAttachment
	for _, attachment := range recordAttachments(record) {
		result = append(result, attachment.metadata)
	}
	return result
}

func readPluginSessionAttachment(ctx context.Context, home, pluginID string, params pluginhost.SessionAttachmentReadParams) (pluginhost.SessionAttachmentReadResult, error) {
	if params.Seq < 1 || params.Index < 0 || params.Offset < 0 || len(params.SHA256) != 64 {
		return pluginhost.SessionAttachmentReadResult{}, errors.New("invalid attachment reference or offset")
	}
	dir, metadata, err := loadVisibleHistorySession(home, pluginID, params.SessionID)
	if err != nil {
		return pluginhost.SessionAttachmentReadResult{}, err
	}
	page, err := session.ReadHistoryQuery(ctx, dir, session.HistoryReadQuery{SessionID: metadata.ID, StartSeq: params.Seq, EndSeq: params.Seq, Limit: 1})
	if err != nil {
		return pluginhost.SessionAttachmentReadResult{}, err
	}
	if len(page.Records) != 1 || page.Records[0].Seq != params.Seq {
		return pluginhost.SessionAttachmentReadResult{}, errors.New("attachment source was removed")
	}
	for _, attachment := range recordAttachments(page.Records[0]) {
		meta := attachment.metadata
		if meta.Kind != params.Kind || meta.Index != params.Index || meta.SHA256 != params.SHA256 {
			continue
		}
		if meta.Size > MaxClientInputAttachmentBytes {
			return pluginhost.SessionAttachmentReadResult{}, errors.New("attachment exceeds the 20 MB client download limit")
		}
		data := attachment.data
		if attachment.uri != "" {
			uri, err := url.Parse(attachment.uri)
			if err != nil {
				return pluginhost.SessionAttachmentReadResult{}, err
			}
			bytes, media, err := ReadManagedArtifact(home, filepath.Join(home, "workspaces", uri.Hostname()), metadata.ID, attachment.uri, MaxClientInputAttachmentBytes)
			if err != nil {
				return pluginhost.SessionAttachmentReadResult{}, err
			}
			sum := sha256.Sum256(bytes)
			if media != meta.MIMEType || hex.EncodeToString(sum[:]) != meta.SHA256 || int64(len(bytes)) != meta.Size {
				return pluginhost.SessionAttachmentReadResult{}, errors.New("artifact no longer matches persisted history")
			}
			data = base64.StdEncoding.EncodeToString(bytes)
		}
		if params.Offset > len(data) {
			return pluginhost.SessionAttachmentReadResult{}, errors.New("attachment offset exceeds its size")
		}
		return pluginhost.SessionAttachmentReadResult{Data: data[params.Offset:min(len(data), params.Offset+128*1024)], Offset: params.Offset, Total: len(data), ContentType: meta.MIMEType, Name: meta.Name, SHA256: meta.SHA256}, nil
	}
	return pluginhost.SessionAttachmentReadResult{}, errors.New("attachment changed or was removed")
}
