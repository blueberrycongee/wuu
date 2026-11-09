package executionworker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const maxExportBytes = 256 * 1024 * 1024

type exportFile struct {
	file *os.File
	size int64
	name string
}

func (s *Server) publish(ctx context.Context, request tools.ArtifactPublishRequest) (toolresult.ContentPart, error) {
	if err := ctx.Err(); err != nil {
		return toolresult.ContentPart{}, err
	}
	info, err := os.Stat(request.Path)
	if err != nil {
		return toolresult.ContentPart{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxExportBytes {
		return toolresult.ContentPart{}, errors.New("artifact must be a regular file of at most 256 MiB")
	}
	source, err := os.Open(request.Path)
	if err != nil {
		return toolresult.ContentPart{}, err
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil {
		return toolresult.ContentPart{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return toolresult.ContentPart{}, errors.New("artifact changed while opening")
	}
	snapshot, err := os.CreateTemp("", "wuu-export-*")
	if err != nil {
		return toolresult.ContentPart{}, err
	}
	fail := func(err error) (toolresult.ContentPart, error) {
		_ = snapshot.Close()
		_ = os.Remove(snapshot.Name())
		return toolresult.ContentPart{}, err
	}
	size, err := io.Copy(snapshot, io.LimitReader(source, maxExportBytes+1))
	if err != nil {
		return fail(err)
	}
	if size > maxExportBytes {
		return fail(errors.New("artifact grew beyond 256 MiB"))
	}
	token := rand.Text()
	s.mu.Lock()
	if s.exports == nil {
		s.exports = make(map[string]exportFile)
	}
	s.exports[token] = exportFile{snapshot, size, filepath.Base(request.Path)}
	s.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"execution_export": token, "name": filepath.Base(request.Path), "size": size})
	return toolresult.ContentPart{Type: toolresult.ContentTypeText, Text: string(data)}, nil
}

func (s *Server) export(method string, data json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Token  string `json:"token"`
		Offset int64  `json:"offset"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, ok := s.exports[request.Token]
	if !ok {
		return nil, errors.New("unknown artifact export")
	}
	if method == "export/close" {
		delete(s.exports, request.Token)
		_ = file.file.Close()
		_ = os.Remove(file.file.Name())
		return json.RawMessage(`{}`), nil
	}
	if request.Offset < 0 || request.Offset > file.size {
		return nil, errors.New("invalid artifact offset")
	}
	buf := make([]byte, 256*1024)
	n, err := file.file.ReadAt(buf, request.Offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return json.Marshal(struct {
		Data []byte `json:"data"`
		EOF  bool   `json:"eof"`
	}{buf[:n], request.Offset+int64(n) == file.size})
}
