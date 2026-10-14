// Package workspaceview provides bounded, read-only access inside an opened workspace root.
package workspaceview

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	MethodWorkspaceDirectoryList = "workspace/directory/list"
	MethodWorkspaceFileRead      = "workspace/file/read"
	MethodWorkspaceFileChunk     = "workspace/file/chunk"
	MethodWorkspaceFileResolve   = "workspace/file/resolve"
	PreviewBytes                 = 512 * 1024
	MediaBytes                   = 2 * 1024 * 1024
	DirectoryEntries             = 4000
	ReferenceVisits              = 8000
)

type Target struct {
	ThreadID      string `json:"thread_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	ConnectionID  string `json:"connection_id,omitempty"`
}

type Params struct {
	ThreadID  string  `json:"thread_id,omitempty"`
	Target    *Target `json:"target,omitempty"`
	Root      string  `json:"root,omitempty"`
	Path      string  `json:"path,omitempty"`
	Reference string  `json:"reference,omitempty"`
	Offset    int64   `json:"offset,omitempty"`
	Version   string  `json:"version,omitempty"`
}

type FileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type DirectoryResult struct {
	Target    *Target     `json:"target,omitempty"`
	Root      string      `json:"root"`
	Path      string      `json:"path"`
	Entries   []FileEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

type FileResult struct {
	Target         *Target `json:"target,omitempty"`
	Root           string  `json:"root"`
	Path           string  `json:"path"`
	AbsolutePath   string  `json:"absolute_path"`
	SizeBytes      int64   `json:"size_bytes"`
	MtimeMS        float64 `json:"mtime_ms"`
	Version        string  `json:"version"`
	SHA256         string  `json:"sha256"`
	Binary         bool    `json:"binary"`
	Truncated      bool    `json:"truncated"`
	Text           *string `json:"text,omitempty"`
	RenderableURL  string  `json:"renderable_url,omitempty"`
	RenderableKind string  `json:"renderable_kind,omitempty"`
}

type ReferenceResult struct {
	Target       *Target  `json:"target,omitempty"`
	Root         string   `json:"root"`
	Reference    string   `json:"reference"`
	Status       string   `json:"status"`
	Path         string   `json:"path,omitempty"`
	AbsolutePath string   `json:"absolute_path,omitempty"`
	Matches      []string `json:"matches,omitempty"`
}

type ChunkResult struct {
	Target  *Target `json:"target,omitempty"`
	Data    string  `json:"data"`
	Offset  int64   `json:"offset"`
	Size    int64   `json:"size"`
	Version string  `json:"version"`
	MIME    string  `json:"mime"`
}

// Bound each encrypted RPC independently; a large download cannot monopolize
// a frame or silently concatenate revisions changed during the transfer.
func ReadChunk(root *os.Root, params Params) (ChunkResult, error) {
	var result ChunkResult
	path, err := RelativePath(params.Path, false)
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

func RelativePath(path string, directory bool) (string, error) {
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

func ListDirectory(root *os.Root, path string) (DirectoryResult, error) {
	result := DirectoryResult{Root: root.Name(), Entries: []FileEntry{}}
	path, err := RelativePath(path, true)
	if err != nil {
		return result, err
	}
	dir, err := root.Open(path)
	if err != nil {
		return result, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(DirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return result, err
	}
	result.Truncated = len(entries) > DirectoryEntries
	if result.Truncated {
		entries = entries[:DirectoryEntries]
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
		result.Entries = append(result.Entries, FileEntry{Name: entry.Name(), Path: relative, Kind: kind})
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].Kind != result.Entries[j].Kind {
			return result.Entries[i].Kind == "directory"
		}
		return result.Entries[i].Name < result.Entries[j].Name
	})
	return result, nil
}

func ReadPreview(root *os.Root, path string) (FileResult, error) {
	result := FileResult{Root: root.Name()}
	path, err := RelativePath(path, false)
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
	data, err := io.ReadAll(io.LimitReader(file, MediaBytes+1))
	if err != nil {
		return result, err
	}
	after, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		return result, errors.New("file changed; reload the preview")
	}
	result.Path = filepath.ToSlash(path)
	result.AbsolutePath = filepath.Join(root.Name(), path)
	result.SizeBytes = info.Size()
	result.Version = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	result.MtimeMS = float64(info.ModTime().UnixNano()) / 1e6
	result.Truncated = len(data) > PreviewBytes
	preview := data
	if len(preview) > PreviewBytes {
		preview = preview[:PreviewBytes]
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
	case "video/mp4", "video/webm", "video/ogg":
		result.RenderableKind = "video"
	}
	if len(data) <= MediaBytes {
		if result.RenderableKind != "" {
			result.RenderableURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
	}
	return result, nil
}

var workspaceReferenceLineSuffix = regexp.MustCompile(`(?i)(:\d+(?:[-:–—]\d+)?|\s+\((?:line|lines)\s+\d+(?:[-:–—]\d+)?\))$`)

func ResolveReference(root *os.Root, reference string) (ReferenceResult, error) {
	result := ReferenceResult{Root: root.Name(), Reference: reference, Status: "invalid"}
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
	path, err := RelativePath(path, false)
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
	directories := []string{"."}
	for len(directories) > 0 && len(matches) < 2 {
		directory := directories[0]
		directories = directories[1:]
		file, err := root.Open(directory)
		if err != nil {
			continue
		}
		entries, readErr := file.ReadDir(ReferenceVisits - visits + 1)
		_ = file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			continue
		}
		for _, entry := range entries {
			visits++
			if visits > ReferenceVisits {
				truncated = true
				break
			}
			if ignoredWorkspaceEntry(entry.Name(), entry.IsDir()) {
				continue
			}
			name := filepath.Join(directory, entry.Name())
			if entry.IsDir() {
				directories = append(directories, name)
				continue
			}
			if entry.Name() == path && regular(name) {
				matches = append(matches, filepath.ToSlash(name))
				if len(matches) > 1 {
					break
				}
			}
		}
		if truncated {
			break
		}
	}
	if len(matches) > 1 || truncated {
		result.Status = "ambiguous"
		result.Matches = matches
	} else if len(matches) == 1 {
		resolve(matches[0])
	}
	return result, nil
}

// Read never creates the root or follows paths outside it. Callers own identity validation.
func Read(rootPath, method string, params Params, target *Target) (any, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// A session file link may be absolute in the worker filesystem. Resolve it
	// here, never against the client or host process's working directory.
	if filepath.IsAbs(params.Path) {
		params.Path, err = filepath.Rel(root.Name(), params.Path)
		if err != nil {
			return nil, err
		}
	}
	switch method {
	case MethodWorkspaceDirectoryList:
		result, err := ListDirectory(root, params.Path)
		result.Target = target
		return result, err
	case MethodWorkspaceFileRead:
		result, err := ReadPreview(root, params.Path)
		result.Target = target
		return result, err
	case MethodWorkspaceFileChunk:
		result, err := ReadChunk(root, params)
		result.Target = target
		return result, err
	case MethodWorkspaceFileResolve:
		result, err := ResolveReference(root, params.Reference)
		result.Target = target
		return result, err
	default:
		return nil, errors.New("unsupported workspace view operation")
	}
}
