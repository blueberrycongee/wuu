package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Use the Markdown syntax tree so code examples and ordinary links never grant
// file access. The reference names a visible message, not an arbitrary path.
func markdownImageSources(body string) []string {
	if len(body) > 128*1024 || !strings.Contains(body, "![") {
		return nil
	}
	document := goldmark.New().Parser().Parse(text.NewReader([]byte(body)))
	var result []string
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if image, ok := node.(*ast.Image); ok && entering {
			source := string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(image.Destination))))
			if len(source) <= 4096 && !slices.Contains(result, source) {
				result = append(result, source)
			}
			if len(result) == 8 {
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return result
}

func markdownImageMedia(source string) string {
	path := source
	if parsed, err := url.Parse(source); err == nil {
		if parsed.Scheme != "" && parsed.Scheme != "file" {
			return ""
		}
		if parsed.Host != "" && parsed.Host != "localhost" {
			return ""
		}
		path = parsed.Path
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

func markdownImageReferences(kind, scopeID, turnID, messageID, body string) []ThreadItemImage {
	var images []ThreadItemImage
	for _, source := range markdownImageSources(body) {
		media := markdownImageMedia(source)
		if media == "" {
			continue
		}
		ref, _ := json.Marshal([]string{kind, scopeID, turnID, messageID, source})
		images = append(images, ThreadItemImage{MediaType: media, RemoteRef: "markdown:" + base64.RawURLEncoding.EncodeToString(ref)})
	}
	return images
}

func (s *Server) handleMarkdownImageRead(ctx context.Context, req Request) error {
	var params struct {
		Kind      string `json:"kind"`
		ScopeID   string `json:"scope_id"`
		TurnID    string `json:"turn_id"`
		MessageID string `json:"message_id"`
		Source    string `json:"source"`
		Seq       int64  `json:"seq"`
		Offset    int    `json:"offset"`
		Preview   bool   `json:"preview"`
		SHA256    string `json:"sha256"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.Offset < 0 || (params.SHA256 != "" && len(params.SHA256) != 64) {
		return s.writeResponse(req.ID, nil, errors.New("invalid image offset or digest"))
	}
	var body, cwd string
	switch params.Kind {
	case "channel":
		if s.channelService == nil || params.Seq <= 0 || params.Seq == 1<<63-1 {
			return s.writeResponse(req.ID, nil, errors.New("invalid room message"))
		}
		messages, err := s.channelService.ListMessageWindow(ctx, channels.RoomHistoryQuery{RoomID: params.ScopeID, AfterSeq: params.Seq - 1, BeforeSeq: params.Seq + 1, Limit: 1})
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if len(messages) == 1 && messages[0].ID == params.MessageID && messages[0].AuthorType == channels.MemberAgent {
			body, cwd = messages[0].Body, s.rt.RootDir
			if ref := messages[0].SourceSessionRef; ref != "" {
				cwd = ""
				if th, err := s.historyThread(ref); err == nil {
					th.mu.Lock()
					cwd = th.CWD
					th.mu.Unlock()
				}
			}
		}
	case "thread":
		th, err := s.historyThread(params.ScopeID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		th.mu.Lock()
		cwd = th.CWD
		for _, turn := range th.Turns {
			if turn.ID != params.TurnID {
				continue
			}
			for _, item := range turn.Items {
				if item.ID == params.MessageID && item.Type == ThreadItemAgentMessage {
					body = item.Text
					break
				}
			}
			break
		}
		th.mu.Unlock()
	}
	media := markdownImageMedia(params.Source)
	if media == "" || !slices.Contains(markdownImageSources(body), params.Source) {
		return s.writeResponse(req.ID, nil, errors.New("image is not attached to this reply"))
	}
	path := params.Source
	if parsed, err := url.Parse(path); err == nil {
		path = parsed.Path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		path = filepath.Join(home, path[2:])
	} else if !filepath.IsAbs(path) {
		if cwd == "" {
			return s.writeResponse(req.ID, nil, errors.New("reply workspace is unavailable"))
		}
		path = filepath.Join(cwd, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 12*1024*1024 {
		return s.writeResponse(req.ID, nil, errors.New("image is unavailable or exceeds 12 MB"))
	}
	bytes, err := io.ReadAll(io.LimitReader(file, 12*1024*1024+1))
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if len(bytes) > 12*1024*1024 || http.DetectContentType(bytes) != media {
		return s.writeResponse(req.ID, nil, errors.New("invalid image contents"))
	}
	data := base64.StdEncoding.EncodeToString(bytes)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(media+"\x00"+data)))
	if params.SHA256 != "" && params.SHA256 != digest {
		return s.writeResponse(req.ID, nil, errors.New("image changed while reading; try again"))
	}
	result, err := readAttachmentChunk(data, media, params.Offset, params.Preview)
	result.SHA256 = digest
	return s.writeResponse(req.ID, result, err)
}
