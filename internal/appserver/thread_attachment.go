package appserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func (s *Server) handleThreadAttachmentRead(req Request) error {
	var params struct {
		ThreadID string `json:"thread_id"`
		TurnID   string `json:"turn_id"`
		ItemID   string `json:"item_id"`
		Index    int    `json:"index"`
		Kind     string `json:"kind"`
		SHA256   string `json:"sha256"`
		Offset   int    `json:"offset"`
		Preview  bool   `json:"preview"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.Index < 0 || params.Offset < 0 || len(params.SHA256) != 64 {
		return s.writeResponse(req.ID, nil, errors.New("invalid attachment reference or offset"))
	}
	th, err := s.historyThread(params.ThreadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var found ThreadItemImage
	var content *ThreadItem
	th.mu.Lock()
	for _, turn := range th.Turns {
		if turn.ID != params.TurnID {
			continue
		}
		for _, item := range turn.Items {
			if item.ID == params.ItemID {
				if req.Method == "thread/content/read" {
					copy := cloneThreadItem(item)
					content = &copy
				} else if params.Kind == "result" && item.ResultDetail != nil && params.Index < len(item.ResultDetail.Content) {
					part := item.ResultDetail.Content[params.Index]
					if part.Type == "image" {
						found = ThreadItemImage{MediaType: part.MIMEType, Data: part.Data}
					}
				} else if params.Kind == "" && params.Index < len(item.Images) {
					found = item.Images[params.Index]
				}
				break
			}
		}
		break
	}
	th.mu.Unlock()
	hash := sha256.New()
	if content != nil {
		raw, _ := json.Marshal(content)
		hash.Write(raw)
		found = ThreadItemImage{MediaType: "application/json", Data: base64.StdEncoding.EncodeToString(raw)}
	} else {
		hash.Write([]byte(found.MediaType))
		hash.Write([]byte{0})
		hash.Write([]byte(found.Data))
	}
	if found.Data == "" || hex.EncodeToString(hash.Sum(nil)) != params.SHA256 {
		return s.writeResponse(req.ID, nil, errors.New("attachment changed or was removed"))
	}
	data, mediaType := found.Data, found.MediaType
	if params.Preview {
		data, err = attachmentThumbnail(data)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		mediaType = "image/jpeg"
	}
	if params.Offset > len(data) {
		return s.writeResponse(req.ID, nil, errors.New("attachment offset exceeds its size"))
	}
	return s.writeResponse(req.ID, struct {
		Data      string `json:"data"`
		Total     int    `json:"total"`
		Offset    int    `json:"offset"`
		MediaType string `json:"content_type"`
	}{data[params.Offset:min(len(data), params.Offset+128*1024)], len(data), params.Offset, mediaType}, nil)
}

func attachmentThumbnail(data string) (string, error) {
	// Check dimensions before decoding: a tiny compressed image can require a
	// huge bitmap. Unsupported formats retain explicit original-image loading.
	config, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	if err != nil {
		return "", err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return "", errors.New("image is too large to preview")
	}
	source, _, err := image.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	if err != nil {
		return "", err
	}
	w, h := config.Width, config.Height
	if w > 384 || h > 384 {
		if w >= h {
			h = max(1, h*384/w)
			w = 384
		} else {
			w = max(1, w*384/h)
			h = 384
		}
	}
	preview := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(preview, preview.Bounds(), image.White, image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(preview, preview.Bounds(), source, source.Bounds(), draw.Over, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, preview, &jpeg.Options{Quality: 75}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}
