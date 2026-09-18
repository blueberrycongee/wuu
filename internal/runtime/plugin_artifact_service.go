package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

const (
	maxImportedArtifactBytes     int64 = 256 * 1024 * 1024
	maxInlineArtifactImportBytes int64 = 2 * 1024 * 1024
)

type managedArtifactManifest struct {
	Version            int    `json:"version"`
	ID                 string `json:"id"`
	ThreadID           string `json:"thread_id"`
	ExecutionID        string `json:"execution_id"`
	CallID             string `json:"call_id,omitempty"`
	PluginID           string `json:"plugin_id"`
	Name               string `json:"name"`
	MIMEType           string `json:"mime_type"`
	Size               int64  `json:"size"`
	SHA256             string `json:"sha256"`
	SourceKind         string `json:"source_kind"`
	SourceName         string `json:"source_name,omitempty"`
	ForkedFromThreadID string `json:"forked_from_thread_id,omitempty"`
}

type artifactImportInvoker struct {
	parent *kernelHostServices
}

func (k *artifactImportInvoker) ID() string                { return k.parent.ID() }
func (k *artifactImportInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *artifactImportInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "kernel service method is unavailable")
	}
	k.parent.mu.RLock()
	executions := k.parent.executions
	stateDir := k.parent.workspaceStateDir
	wuuHome := k.parent.wuuHome
	k.parent.mu.RUnlock()
	if executions == nil || strings.TrimSpace(stateDir) == "" || strings.TrimSpace(wuuHome) == "" {
		return nil, serviceError("service_unavailable", "artifact storage is unavailable")
	}
	scope, scopeErr := executions.ResolveToolExecution(params.Caller, params.ExecutionID)
	if scopeErr != nil {
		return nil, scopeErr
	}
	var input pluginhost.ArtifactImportParams
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	result, err := importPluginArtifact(ctx, wuuHome, stateDir, scope, input)
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(result)
}

func importPluginArtifact(ctx context.Context, wuuHome, stateDir string, scope pluginhost.ToolExecutionScope, input pluginhost.ArtifactImportParams) (pluginhost.ArtifactImportResult, error) {
	return importArtifact(ctx, wuuHome, stateDir, scope, input, nil)
}

// An optional open file lets built-in publishers inspect and copy the same
// descriptor, rather than reopening a path after MIME validation.
func importArtifact(ctx context.Context, wuuHome, stateDir string, scope pluginhost.ToolExecutionScope, input pluginhost.ArtifactImportParams, opened *os.File) (pluginhost.ArtifactImportResult, error) {
	pathValue := strings.TrimSpace(input.Path)
	dataValue := strings.TrimSpace(input.Data)
	if (pathValue == "") == (dataValue == "") {
		return pluginhost.ArtifactImportResult{}, serviceError("invalid_params", "artifact import requires exactly one of path or data")
	}

	relState, err := managedWorkspaceKey(wuuHome, stateDir)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}

	name := strings.TrimSpace(input.Name)
	sourceKind := "inline"
	sourceName := name
	copyLimit := maxImportedArtifactBytes
	var source io.ReadCloser
	if pathValue != "" {
		sourceKind = "path"
		sourceName = filepath.Base(pathValue)
		if !filepath.IsAbs(pathValue) {
			if strings.TrimSpace(scope.CWD) == "" {
				return pluginhost.ArtifactImportResult{}, serviceError("invalid_params", "relative artifact path requires an execution working directory")
			}
			pathValue = filepath.Join(scope.CWD, pathValue)
		}
		file := opened
		if file == nil {
			var openErr error
			file, openErr = os.Open(filepath.Clean(pathValue))
			if openErr != nil {
				return pluginhost.ArtifactImportResult{}, serviceError("artifact_unavailable", fmt.Sprintf("open artifact: %v", openErr))
			}
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			return pluginhost.ArtifactImportResult{}, serviceError("invalid_params", "artifact path must name a regular file")
		}
		if info.Size() > maxImportedArtifactBytes {
			_ = file.Close()
			return pluginhost.ArtifactImportResult{}, serviceError("limit_exceeded", fmt.Sprintf("artifact exceeds %d bytes", maxImportedArtifactBytes))
		}
		source = file
		if name == "" {
			name = filepath.Base(pathValue)
		}
	} else {
		copyLimit = maxInlineArtifactImportBytes
		if int64(len(dataValue)) > int64(base64.StdEncoding.EncodedLen(int(copyLimit)))+4 {
			return pluginhost.ArtifactImportResult{}, serviceError("limit_exceeded", fmt.Sprintf("inline artifact exceeds %d bytes", copyLimit))
		}
		source = io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(dataValue)))
	}
	defer source.Close()

	name = safeArtifactName(name)
	artifactID, err := randomArtifactID()
	if err != nil {
		return pluginhost.ArtifactImportResult{}, fmt.Errorf("create artifact id: %w", err)
	}
	dir := filepath.Join(statepath.SessionArtifactDir(stateDir, scope.ThreadID), "artifacts", artifactID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return pluginhost.ArtifactImportResult{}, fmt.Errorf("create artifact directory: %w", err)
	}
	destination := filepath.Join(dir, name)
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return pluginhost.ArtifactImportResult{}, fmt.Errorf("create managed artifact: %w", err)
	}
	hash := sha256.New()
	limited := &io.LimitedReader{R: source, N: copyLimit + 1}
	written, copyErr := io.Copy(io.MultiWriter(file, hash), limited)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > copyLimit {
		_ = os.RemoveAll(dir)
		if written > copyLimit {
			return pluginhost.ArtifactImportResult{}, serviceError("limit_exceeded", fmt.Sprintf("artifact exceeds %d bytes", copyLimit))
		}
		return pluginhost.ArtifactImportResult{}, fmt.Errorf("copy managed artifact: %w", firstArtifactError(copyErr, closeErr))
	}
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(dir)
		return pluginhost.ArtifactImportResult{}, err
	}

	mimeType := strings.TrimSpace(input.MIMEType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(name))
	}
	if mimeType == "" {
		if probeFile, openErr := os.Open(destination); openErr == nil {
			probe := make([]byte, 512)
			count, _ := probeFile.Read(probe)
			_ = probeFile.Close()
			mimeType = http.DetectContentType(probe[:count])
		}
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	sourceName = strings.TrimSpace(sourceName)
	if sourceName != "" {
		sourceName = filepath.Base(sourceName)
	}
	manifest, err := json.Marshal(managedArtifactManifest{
		Version: 1, ID: artifactID, ThreadID: scope.ThreadID, ExecutionID: scope.ID,
		CallID: scope.CallID, PluginID: scope.PluginID, Name: name, MIMEType: mimeType,
		Size: written, SHA256: checksum, SourceKind: sourceKind, SourceName: sourceName,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return pluginhost.ArtifactImportResult{}, fmt.Errorf("encode artifact metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".artifact.json"), manifest, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return pluginhost.ArtifactImportResult{}, fmt.Errorf("write artifact metadata: %w", err)
	}
	rendererURL := &url.URL{Scheme: "wuu-artifact", Host: relState, Path: "/" + strings.Join([]string{scope.ThreadID, artifactID, name}, "/")}
	rendererURL.RawQuery = url.Values{"sha256": []string{checksum}}.Encode()
	uri := rendererURL.String()
	return pluginhost.ArtifactImportResult{
		ID: artifactID, URI: uri, Name: name, MIMEType: mimeType, Size: written, SHA256: checksum,
	}, nil
}

func (k *kernelHostServices) materializeToolResult(ctx context.Context, scope pluginhost.ToolExecutionScope, result *toolresult.Result) error {
	if result == nil {
		return nil
	}
	k.mu.RLock()
	stateDir := k.workspaceStateDir
	wuuHome := k.wuuHome
	k.mu.RUnlock()
	for index := range result.Content {
		part := &result.Content[index]
		switch part.Type {
		case toolresult.ContentTypeImage, toolresult.ContentTypeAudio, toolresult.ContentTypeFile:
		default:
			continue
		}
		managed, isManaged, err := resolveManagedArtifact(wuuHome, stateDir, scope.ThreadID, part.URI)
		if err != nil {
			return fmt.Errorf("content[%d]: %w", index, err)
		}
		if isManaged {
			applyManagedArtifact(part, managed)
			continue
		}
		path, local, err := localArtifactPath(part.URI, scope.CWD)
		if err != nil {
			return fmt.Errorf("content[%d]: %w", index, err)
		}
		if !local {
			continue
		}
		artifact, err := importPluginArtifact(ctx, wuuHome, stateDir, scope, pluginhost.ArtifactImportParams{
			Path: path, Name: part.Name, MIMEType: part.MIMEType,
		})
		if err != nil {
			return fmt.Errorf("content[%d]: %w", index, err)
		}
		applyManagedArtifact(part, artifact)
	}
	return nil
}

func managedWorkspaceKey(wuuHome, stateDir string) (string, error) {
	workspaceRoot := filepath.Join(filepath.Clean(wuuHome), "workspaces")
	relState, err := filepath.Rel(workspaceRoot, filepath.Clean(stateDir))
	if err != nil || relState == "." || relState == ".." || strings.HasPrefix(relState, ".."+string(filepath.Separator)) || strings.Contains(relState, string(filepath.Separator)) {
		return "", serviceError("service_unavailable", "artifact storage is outside the managed workspace root")
	}
	return relState, nil
}

func resolveManagedArtifact(wuuHome, stateDir, threadID, raw string) (pluginhost.ArtifactImportResult, bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "wuu-artifact") {
		return pluginhost.ArtifactImportResult{}, false, nil
	}
	workspaceKey, err := managedWorkspaceKey(wuuHome, stateDir)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, true, err
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if !strings.EqualFold(parsed.Host, workspaceKey) || len(segments) != 3 || segments[0] != threadID || !validArtifactID(segments[1]) {
		return pluginhost.ArtifactImportResult{}, true, serviceError("invalid_params", "managed artifact uri is outside the current thread")
	}
	artifactID, name := segments[1], segments[2]
	if safeArtifactName(name) != name {
		return pluginhost.ArtifactImportResult{}, true, serviceError("invalid_params", "managed artifact uri has an invalid file name")
	}
	dir := filepath.Join(statepath.SessionArtifactDir(stateDir, threadID), "artifacts", artifactID)
	rawManifest, err := os.ReadFile(filepath.Join(dir, ".artifact.json"))
	if err != nil {
		return pluginhost.ArtifactImportResult{}, true, serviceError("artifact_unavailable", "managed artifact metadata is unavailable")
	}
	var manifest managedArtifactManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil || manifest.Version != 1 || manifest.ID != artifactID || manifest.ThreadID != threadID || manifest.Name != name {
		return pluginhost.ArtifactImportResult{}, true, serviceError("artifact_invalid", "managed artifact metadata does not match its uri")
	}
	digests := parsed.Query()["sha256"]
	if len(parsed.Query()) != 1 || len(digests) != 1 || digests[0] != manifest.SHA256 {
		return pluginhost.ArtifactImportResult{}, true, serviceError("artifact_invalid", "managed artifact uri does not identify the stored version")
	}
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.Size {
		return pluginhost.ArtifactImportResult{}, true, serviceError("artifact_unavailable", "managed artifact file is unavailable or changed")
	}
	return pluginhost.ArtifactImportResult{
		ID: manifest.ID, URI: parsed.String(), Name: manifest.Name, MIMEType: manifest.MIMEType,
		Size: manifest.Size, SHA256: manifest.SHA256,
	}, true, nil
}

func applyManagedArtifact(part *toolresult.ContentPart, artifact pluginhost.ArtifactImportResult) {
	part.URI = artifact.URI
	part.Name = artifact.Name
	part.MIMEType = artifact.MIMEType
	presentation := toolresult.ArtifactPresentation{}
	if part.Artifact != nil {
		presentation = *part.Artifact
	}
	presentation.Ref = artifact.ID
	presentation.SHA256 = artifact.SHA256
	presentation.SizeBytes = &artifact.Size
	part.Artifact = &presentation
}

func validArtifactID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func localArtifactPath(raw, cwd string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false, serviceError("invalid_params", fmt.Sprintf("invalid artifact uri: %v", err))
	}
	switch strings.ToLower(parsed.Scheme) {
	case "":
		path := raw
		if !filepath.IsAbs(path) {
			if strings.TrimSpace(cwd) == "" {
				return "", false, serviceError("invalid_params", "relative artifact path requires an execution working directory")
			}
			path = filepath.Join(cwd, path)
		}
		return filepath.Clean(path), true, nil
	case "file":
		if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
			return "", false, serviceError("invalid_params", "file artifact uri must be local")
		}
		path, unescapeErr := url.PathUnescape(parsed.Path)
		if unescapeErr != nil {
			return "", false, serviceError("invalid_params", "invalid file artifact uri")
		}
		path = filepath.FromSlash(path)
		if goruntime.GOOS == "windows" && len(path) >= 3 && path[0] == filepath.Separator && path[2] == ':' {
			path = path[1:]
		}
		return filepath.Clean(path), true, nil
	case "wuu-file":
		return "", false, serviceError("invalid_params", "wuu-file is not a durable artifact uri; return a file path or use host.artifact.import")
	default:
		return "", false, nil
	}
}

func safeArtifactName(input string) string {
	name := filepath.Base(strings.TrimSpace(input))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return '-'
		}
		return r
	}, name)
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "artifact.bin"
	}
	if name == ".artifact.json" {
		name = "artifact.json"
	}
	if len([]byte(name)) > 160 {
		sum := sha256.Sum256([]byte(name))
		ext := filepath.Ext(name)
		if len([]byte(ext)) > 24 {
			ext = ""
		}
		name = "artifact-" + hex.EncodeToString(sum[:8]) + ext
	}
	return name
}

func randomArtifactID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func firstArtifactError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
