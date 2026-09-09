package appserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

const (
	MethodWorkspaceDirectoryList = "workspace/directory/list"
	MethodWorkspaceFileRead      = "workspace/file/read"
	MethodWorkspaceFileChunk     = "workspace/file/chunk"
	MethodWorkspaceFileResolve   = "workspace/file/resolve"
	workspacePreviewBytes        = 512 * 1024
	workspaceMediaBytes          = 2 * 1024 * 1024
	workspaceDirectoryEntries    = 4000
	workspaceReferenceVisits     = 8000
)

type workspaceViewParams struct {
	Root      string `json:"root,omitempty"`
	Path      string `json:"path,omitempty"`
	Reference string `json:"reference,omitempty"`
	Offset    int64  `json:"offset,omitempty"`
	Version   string `json:"version,omitempty"`
}

type workspaceFileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type workspaceDirectoryResult struct {
	Root      string               `json:"root"`
	Path      string               `json:"path"`
	Entries   []workspaceFileEntry `json:"entries"`
	Truncated bool                 `json:"truncated"`
}

type workspaceFileResult struct {
	Root           string  `json:"root"`
	Path           string  `json:"path"`
	AbsolutePath   string  `json:"absolute_path"`
	SizeBytes      int64   `json:"size_bytes"`
	MtimeMS        float64 `json:"mtime_ms"`
	SHA256         string  `json:"sha256"`
	Binary         bool    `json:"binary"`
	Truncated      bool    `json:"truncated"`
	Text           *string `json:"text,omitempty"`
	RenderableURL  string  `json:"renderable_url,omitempty"`
	RenderableKind string  `json:"renderable_kind,omitempty"`
}

type workspaceReferenceResult struct {
	Root         string   `json:"root"`
	Reference    string   `json:"reference"`
	Status       string   `json:"status"`
	Path         string   `json:"path,omitempty"`
	AbsolutePath string   `json:"absolute_path,omitempty"`
	Matches      []string `json:"matches,omitempty"`
}

func (s *Server) handleWorkspaceView(req Request) error {
	var params workspaceViewParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	rootPath, err := s.workspaceViewRoot(params.Root)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer root.Close()
	var result any
	switch req.Method {
	case MethodWorkspaceDirectoryList:
		result, err = listWorkspaceDirectory(root, params.Path)
	case MethodWorkspaceFileRead:
		result, err = readWorkspacePreview(root, params.Path)
	case MethodWorkspaceFileChunk:
		result, err = readWorkspaceChunk(root, params)
	case MethodWorkspaceFileResolve:
		result, err = resolveWorkspaceReference(root, params.Reference)
	}
	return s.writeResponse(req.ID, result, err)
}

type workspaceChunkResult struct {
	Data    string `json:"data"`
	Offset  int64  `json:"offset"`
	Size    int64  `json:"size"`
	Version string `json:"version"`
	MIME    string `json:"mime"`
}

// Bound each encrypted RPC independently; a large download cannot monopolize
// a frame or silently concatenate revisions changed during the transfer.
func readWorkspaceChunk(root *os.Root, params workspaceViewParams) (workspaceChunkResult, error) {
	var result workspaceChunkResult
	path, err := workspaceRelativePath(params.Path, false)
	if err != nil {
		return result, err
	}
	info, err := root.Stat(path)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("selected path is not a regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024*1024 {
		return result, errors.New("file must be regular and at most 64 MB")
	}
	version := fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	if params.Offset < 0 || params.Offset > info.Size() || (params.Offset > 0 && params.Version == "") {
		return result, errors.New("invalid file offset or missing version")
	}
	if params.Version != "" && params.Version != version {
		return result, errors.New("file changed; restart the download")
	}
	header := make([]byte, 512)
	n, err := f.ReadAt(header, 0)
	if err != nil && err != io.EOF {
		return result, err
	}
	result.MIME = http.DetectContentType(header[:n])
	data := make([]byte, min(int64(256*1024), info.Size()-params.Offset))
	n, err = f.ReadAt(data, params.Offset)
	if err != nil && err != io.EOF {
		return result, err
	}
	after, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() || n != len(data) {
		return result, errors.New("file changed; restart the download")
	}
	result.Data = base64.StdEncoding.EncodeToString(data)
	result.Offset, result.Size, result.Version = params.Offset, info.Size(), version
	return result, nil
}

// A browser may navigate a registered workspace or a session's worktree. It
// cannot turn a caller-supplied root into a machine-wide file server.
func (s *Server) workspaceViewRoot(requested string) (string, error) {
	if requested == "" {
		requested = s.rt.RootDir
	}
	if !filepath.IsAbs(requested) {
		return "", errors.New("workspace root must be absolute")
	}
	target, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return "", err
	}
	allowed := func(path string) bool {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(real, target)
		return err == nil && (rel == "." || filepath.IsLocal(rel))
	}
	if allowed(s.rt.RootDir) {
		return target, nil
	}
	registered, err := workspaces.List(s.rt.WuuHome)
	if err != nil {
		return "", err
	}
	for _, workspace := range registered {
		if allowed(workspace.Root) {
			return target, nil
		}
	}
	sessions, err := session.List(s.rt.SessionDir, 0)
	if err != nil {
		return "", err
	}
	for _, metadata := range sessions {
		if metadata.CWD != "" && allowed(metadata.CWD) {
			return target, nil
		}
	}
	return "", errors.New("directory is not a known workspace or session directory")
}

func workspaceRelativePath(path string, directory bool) (string, error) {
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" && directory {
		return ".", nil
	}
	if !filepath.IsLocal(path) {
		return "", errors.New("path must stay inside the workspace")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return "", errors.New("path must stay inside the workspace")
		}
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}

func ignoredWorkspaceEntry(name string, directory bool) bool {
	if name == ".DS_Store" {
		return true
	}
	if !directory {
		return false
	}
	switch name {
	case ".git", ".next", ".turbo", ".vite", "coverage", "dist", "node_modules", "out", "target":
		return true
	}
	return false
}

func listWorkspaceDirectory(root *os.Root, path string) (workspaceDirectoryResult, error) {
	result := workspaceDirectoryResult{Root: root.Name(), Entries: []workspaceFileEntry{}}
	path, err := workspaceRelativePath(path, true)
	if err != nil {
		return result, err
	}
	dir, err := root.Open(path)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(workspaceDirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return result, err
	}
	result.Truncated = len(entries) > workspaceDirectoryEntries
	if result.Truncated {
		entries = entries[:workspaceDirectoryEntries]
	}
	if path != "." {
		result.Path = filepath.ToSlash(path)
	}
	for _, entry := range entries {
		if ignoredWorkspaceEntry(entry.Name(), entry.IsDir()) {
			continue
		}
		if !entry.IsDir() && !entry.Type().IsRegular() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		kind := "file"
		relative := filepath.ToSlash(filepath.Join(path, entry.Name()))
		if entry.IsDir() {
			kind = "directory"
			relative += "/"
		}
		result.Entries = append(result.Entries, workspaceFileEntry{Name: entry.Name(), Path: relative, Kind: kind})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].Kind != result.Entries[j].Kind {
			return result.Entries[i].Kind == "directory"
		}
		return result.Entries[i].Name < result.Entries[j].Name
	})
	return result, nil
}

func readWorkspacePreview(root *os.Root, path string) (workspaceFileResult, error) {
	result := workspaceFileResult{Root: root.Name()}
	path, err := workspaceRelativePath(path, false)
	if err != nil {
		return result, err
	}
	info, err := root.Stat(path)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("selected path is not a regular file")
	}
	file, err := root.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("selected path is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, workspaceMediaBytes+1))
	if err != nil {
		return result, err
	}
	result.Path = filepath.ToSlash(path)
	result.AbsolutePath = filepath.Join(root.Name(), path)
	result.SizeBytes = info.Size()
	result.MtimeMS = float64(info.ModTime().UnixNano()) / 1e6
	result.Truncated = len(data) > workspacePreviewBytes
	preview := data
	if len(preview) > workspacePreviewBytes {
		preview = preview[:workspacePreviewBytes]
	}
	hash := sha256.Sum256(preview)
	result.SHA256 = hex.EncodeToString(hash[:])
	result.Binary = bytes.IndexByte(preview, 0) >= 0
	if !result.Binary {
		text := strings.ToValidUTF8(string(preview), "\ufffd")
		result.Text = &text
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		result.RenderableKind = "image"
	case "application/pdf":
		result.RenderableKind = "pdf"
	}
	if len(data) <= workspaceMediaBytes {
		if result.RenderableKind != "" {
			result.RenderableURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
	}
	return result, nil
}

var workspaceReferenceLineSuffix = regexp.MustCompile(`(?i)(:\d+(?:[-:–—]\d+)?|\s+\((?:line|lines)\s+\d+(?:[-:–—]\d+)?\))$`)

func resolveWorkspaceReference(root *os.Root, reference string) (workspaceReferenceResult, error) {
	result := workspaceReferenceResult{Root: root.Name(), Reference: reference, Status: "invalid"}
	path := strings.TrimSpace(reference)
	for _, pair := range [][2]string{{"`", "`"}, {"<", ">"}, {"\"", "\""}, {"'", "'"}} {
		if strings.HasPrefix(path, pair[0]) && strings.HasSuffix(path, pair[1]) && len(path) >= 2 {
			path = strings.TrimSpace(path[1 : len(path)-1])
			break
		}
	}
	path = strings.ReplaceAll(workspaceReferenceLineSuffix.ReplaceAllString(path, ""), "\\", "/")
	if path == "" || strings.Contains(path, "\x00") || strings.Contains(path, "://") {
		return result, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return result, err
		}
		path = filepath.Join(home, path[2:])
	}
	qualified := strings.Contains(path, "/")
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(root.Name(), path)
		if err != nil {
			return result, nil
		}
		path = relative
	}
	path, err := workspaceRelativePath(path, false)
	if err != nil {
		return result, nil
	}
	result.Status = "missing"
	regular := func(candidate string) bool {
		info, err := root.Stat(candidate)
		return err == nil && info.Mode().IsRegular()
	}
	resolve := func(path string) {
		result.Status = "resolved"
		result.Path = filepath.ToSlash(path)
		result.AbsolutePath = filepath.Join(root.Name(), path)
	}
	if qualified {
		if regular(path) {
			resolve(path)
		}
		return result, nil
	}
	matches := []string{}
	visits, truncated := 0, false
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		visits++
		if visits > workspaceReferenceVisits {
			truncated = true
			return fs.SkipAll
		}
		if walkErr != nil {
			return nil
		}
		if name != "." && ignoredWorkspaceEntry(entry.Name(), entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && entry.Name() == path && regular(name) {
			matches = append(matches, name)
			if len(matches) > 1 {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if len(matches) > 1 || truncated {
		result.Status = "ambiguous"
		result.Matches = matches
	} else if len(matches) == 1 {
		resolve(matches[0])
	}
	return result, nil
}
