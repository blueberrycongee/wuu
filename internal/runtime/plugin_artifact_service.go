package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/storelock"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"

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
	scope, scopeErr := params.CallerExecution(true)
	if scopeErr != nil {
		return nil, scopeErr
	}
	var input pluginhost.ArtifactImportParams
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	result, err := k.parent.artifactClient(params.ExecutionID).Import(ctx, artifactScope(scope), input, false)
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
	artifactRoot := filepath.Join(statepath.SessionArtifactDir(stateDir, scope.ThreadID), "artifacts")
	lock, err := storelock.AcquireContext(ctx, artifactRoot)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	defer lock.Release()
	dir := filepath.Join(artifactRoot, artifactID)
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
	var client *ArtifactClient
	if k.selectedServices != nil {
		client = k.selectedServices.artifacts
	}
	k.mu.RUnlock()
	for index := range result.Content {
		part := &result.Content[index]
		switch part.Type {
		case toolresult.ContentTypeImage, toolresult.ContentTypeAudio, toolresult.ContentTypeFile:
		default:
			continue
		}
		managed, isManaged, err := client.Resolve(ctx, scope.ThreadID, part.URI)
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
		artifact, err := client.Import(ctx, artifactScope(scope), pluginhost.ArtifactImportParams{
			Path: path, Name: part.Name, MIMEType: part.MIMEType,
		}, false)
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

// ReadManagedArtifact reads a published immutable snapshot within its owning
// workspace and thread. The caller supplies its transport's decoded byte limit.
func ReadManagedArtifact(wuuHome, stateDir, threadID, uri string, maxBytes int64) ([]byte, string, error) {
	artifact, managed, err := resolveManagedArtifact(wuuHome, stateDir, threadID, uri)
	if err != nil {
		return nil, "", err
	}
	if !managed || artifact.Size <= 0 || artifact.Size > maxBytes {
		return nil, "", fmt.Errorf("artifact is unavailable or exceeds %d bytes", maxBytes)
	}
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	file, err := root.Open(filepath.Join("sessions", threadID, "artifacts", artifact.ID, artifact.Name))
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != artifact.Size || hex.EncodeToString(digest[:]) != artifact.SHA256 {
		return nil, "", serviceError("artifact_invalid", "managed artifact contents changed")
	}
	return data, artifact.MIMEType, nil
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

const ArtifactServiceName = "state.artifacts"
const ArtifactModule = "wuu.artifacts.local"

// ArtifactScope is supplied by an admitted native consumer or authenticated
// host.artifact.import gateway. Providers receive no filesystem state root from
// an ordinary plugin request; their root is captured at composition creation.
type ArtifactScope struct {
	ThreadID    string `json:"thread_id"`
	ExecutionID string `json:"execution_id"`
	PluginID    string `json:"plugin_id"`
	CallID      string `json:"call_id,omitempty"`
	CWD         string `json:"cwd,omitempty"`
}
type ArtifactImportRequest struct {
	Scope   ArtifactScope                   `json:"scope"`
	Input   pluginhost.ArtifactImportParams `json:"input"`
	Present bool                            `json:"present,omitempty"`
}
type ArtifactResolveRequest struct {
	ThreadID string `json:"thread_id"`
	URI      string `json:"uri"`
}
type ArtifactResolveResult struct {
	Artifact pluginhost.ArtifactImportResult `json:"artifact"`
	Managed  bool                            `json:"managed"`
}
type ArtifactReadRequest struct {
	ArtifactResolveRequest
	MaxBytes int64 `json:"max_bytes"`
}
type ArtifactReadResult struct {
	Data     []byte `json:"data"`
	MIMEType string `json:"mime_type"`
}

// Range reads are bounded independently of total artifact size. Providers must
// serve the exact immutable digest, not return an entire payload as a fallback.
const MaxArtifactRangeBytes int64 = 1024 * 1024

type ArtifactRangeRequest struct {
	ArtifactReadRequest
	Offset int64  `json:"offset"`
	SHA256 string `json:"sha256"`
}
type ArtifactRangeResult struct {
	ArtifactReadResult
	Offset int64  `json:"offset"`
	Total  int64  `json:"total"`
	SHA256 string `json:"sha256"`
}

type ArtifactForkRequest struct {
	SourceThreadID string `json:"source_thread_id"`
	TargetThreadID string `json:"target_thread_id"`
	URI            string `json:"uri"`
}

// DeleteThread removes current payloads after the caller drains producers.
// It is idempotent even when session documents were already removed.
type ArtifactDeleteThreadRequest struct {
	ThreadID string `json:"thread_id"`
}
type ArtifactDeleteThreadResult struct {
	ThreadID string `json:"thread_id"`
	Deleted  bool   `json:"deleted"`
}

type artifactService struct {
	mu                 sync.RWMutex
	id, home, stateDir string
	state              pluginhost.State
}

func NewArtifactService(id, resolvedWuuHome, stateDir string) *artifactService {
	return &artifactService{id: id, home: resolvedWuuHome, stateDir: stateDir, state: pluginhost.StatePrepared}
}
func (s *artifactService) ID() string { return s.id }
func (s *artifactService) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (*artifactService) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*artifactService) ProvidedServices() []pluginhost.ServiceDescriptor {
	var methods []pluginhost.ServiceMethodDescriptor
	for _, method := range []string{"import", "resolve", "read", "read-range", "fork", "delete-thread"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: method, InputSchema: ArtifactServiceName + "." + method + ".input.v1", OutputSchema: ArtifactServiceName + "." + method + ".output.v1"})
	}
	return []pluginhost.ServiceDescriptor{{Name: ArtifactServiceName, Version: "1.2.0", Methods: methods}}
}
func (s *artifactService) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared {
		return serviceError("service_unavailable", "artifact provider cannot activate")
	}
	if _, err := managedWorkspaceKey(s.home, s.stateDir); err != nil {
		return err
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *artifactService) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func (s *artifactService) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state != pluginhost.StateActive || params.Service != ArtifactServiceName {
		return nil, serviceError("service_unavailable", "artifact provider is unavailable")
	}
	var result any
	var err error
	switch params.Method {
	case "import":
		var request ArtifactImportRequest
		if err := decodeServiceParams(params.Params, &request); err != nil {
			return nil, err
		}
		if !validArtifactThreadID(request.Scope.ThreadID) {
			return nil, serviceError("invalid_params", "artifact thread id is invalid")
		}
		scope := pluginhost.ToolExecutionScope{ExecutionSnapshot: pluginhost.ExecutionSnapshot{ID: request.Scope.ExecutionID, ThreadID: request.Scope.ThreadID, PluginID: request.Scope.PluginID, CallID: request.Scope.CallID, CWD: request.Scope.CWD}}
		var file *os.File
		if request.Present {
			path := request.Input.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(request.Scope.CWD, path)
			}
			info, statErr := os.Stat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				return nil, serviceError("invalid_params", "presented artifact must be a regular file")
			}
			if info.Size() > maxImportedArtifactBytes {
				return nil, serviceError("limit_exceeded", "presented artifact exceeds the size limit")
			}
			file, err = os.Open(path)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			request.Input.MIMEType, err = presentedArtifactMIME(file, path)
			if err != nil {
				return nil, err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}
		result, err = importArtifact(ctx, s.home, s.stateDir, scope, request.Input, file)
	case "resolve":
		var request ArtifactResolveRequest
		if err := decodeServiceParams(params.Params, &request); err != nil {
			return nil, err
		}
		if !validArtifactThreadID(request.ThreadID) {
			return nil, serviceError("invalid_params", "artifact thread id is invalid")
		}
		var resolved ArtifactResolveResult
		resolved.Artifact, resolved.Managed, err = resolveManagedArtifact(s.home, s.stateDir, request.ThreadID, request.URI)
		result = resolved
	case "read":
		var request ArtifactReadRequest
		if err := decodeServiceParams(params.Params, &request); err != nil {
			return nil, err
		}
		if !validArtifactThreadID(request.ThreadID) || request.MaxBytes <= 0 || request.MaxBytes > maxImportedArtifactBytes {
			return nil, serviceError("invalid_params", "artifact read scope or byte limit is invalid")
		}
		var read ArtifactReadResult
		read.Data, read.MIMEType, err = ReadManagedArtifact(s.home, s.stateDir, request.ThreadID, request.URI, request.MaxBytes)
		result = read
	case "read-range":
		var request ArtifactRangeRequest
		if err := decodeServiceParams(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.readRange(ctx, request)
	case "fork":
		var request ArtifactForkRequest
		if err := decodeServiceParams(params.Params, &request); err != nil {
			return nil, err
		}
		result, err = s.fork(ctx, request)
	case "delete-thread":
		var request ArtifactDeleteThreadRequest
		if err := decodeServiceParams(params.Params, &request); err != nil {
			return nil, err
		}
		if !validArtifactThreadID(request.ThreadID) {
			return nil, serviceError("invalid_params", "artifact thread id is invalid")
		}
		result, err = s.deleteThread(ctx, request.ThreadID)
	default:
		return nil, serviceError("method_not_found", "artifact method is unavailable")
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func (s *artifactService) deleteThread(ctx context.Context, threadID string) (ArtifactDeleteThreadResult, error) {
	receipt := ArtifactDeleteThreadResult{ThreadID: threadID}
	root, err := os.OpenRoot(s.stateDir)
	if errors.Is(err, os.ErrNotExist) {
		receipt.Deleted = true
		return receipt, nil
	}
	if err != nil {
		return receipt, serviceError("artifact_unavailable", "artifact cleanup could not open its storage")
	}
	defer root.Close()
	artifacts, err := root.OpenRoot(filepath.Join("sessions", threadID, "artifacts"))
	if errors.Is(err, os.ErrNotExist) {
		receipt.Deleted = true
		return receipt, nil
	}
	if err != nil {
		return receipt, serviceError("artifact_unavailable", "artifact cleanup could not open its thread")
	}
	defer artifacts.Close()
	lock, err := storelock.AcquireContext(ctx, filepath.Join(statepath.SessionArtifactDir(s.stateDir, threadID), "artifacts"))
	if err != nil {
		return receipt, serviceError("artifact_unavailable", "artifact cleanup lock is unavailable")
	}
	defer lock.Release()
	dir, err := artifacts.Open(".")
	if err != nil {
		return receipt, serviceError("artifact_unavailable", "artifact cleanup could not inspect its thread")
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return receipt, serviceError("artifact_unavailable", "artifact cleanup could not inspect its thread")
	}
	for _, entry := range entries {
		// Keep the lock inode so concurrent generations serialize on the same file.
		if entry.Name() == storelock.FileName {
			continue
		}
		if err := ctx.Err(); err != nil {
			return receipt, err
		}
		if err := artifacts.RemoveAll(entry.Name()); err != nil {
			return receipt, serviceError("artifact_unavailable", "artifact cleanup is incomplete; retry the same thread")
		}
	}
	if err := dir.Sync(); err != nil {
		return receipt, serviceError("artifact_unavailable", "artifact cleanup outcome is uncertain; retry the same thread")
	}
	receipt.Deleted = true
	return receipt, nil
}

func validArtifactThreadID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 256 && !strings.ContainsAny(id, "/\\\x00")
}
func (s *artifactService) fork(ctx context.Context, request ArtifactForkRequest) (pluginhost.ArtifactImportResult, error) {
	if !validArtifactThreadID(request.SourceThreadID) || !validArtifactThreadID(request.TargetThreadID) || request.SourceThreadID == request.TargetThreadID {
		return pluginhost.ArtifactImportResult{}, serviceError("invalid_params", "artifact fork requires distinct valid threads")
	}
	artifact, managed, err := resolveManagedArtifact(s.home, s.stateDir, request.SourceThreadID, request.URI)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	if !managed {
		return pluginhost.ArtifactImportResult{}, serviceError("artifact_unavailable", "artifact is not managed by this provider")
	}
	parsed, err := url.Parse(artifact.URI)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	parsed.Path = "/" + strings.Join([]string{request.TargetThreadID, artifact.ID, artifact.Name}, "/")
	targetURI := parsed.String()
	targetRoot := filepath.Join(statepath.SessionArtifactDir(s.stateDir, request.TargetThreadID), "artifacts")
	lock, err := storelock.AcquireContext(ctx, targetRoot)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	defer lock.Release()
	target := filepath.Join(targetRoot, artifact.ID)
	if _, err := os.Stat(target); err == nil {
		// A completed previous copy is an idempotent fork receipt. An incomplete
		// directory is never accepted or overwritten as a successful copy.
		existing, _, err := resolveManagedArtifact(s.home, s.stateDir, request.TargetThreadID, targetURI)
		return existing, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return pluginhost.ArtifactImportResult{}, err
	}
	root, err := os.OpenRoot(s.stateDir)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	defer root.Close()
	source, err := root.Open(filepath.Join("sessions", request.SourceThreadID, "artifacts", artifact.ID, artifact.Name))
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	defer source.Close()
	temp, err := os.MkdirTemp(targetRoot, ".fork-")
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	defer os.RemoveAll(temp)
	output, err := os.OpenFile(filepath.Join(temp, artifact.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(source, maxImportedArtifactBytes+1))
	closeErr := output.Close()
	if err := firstArtifactError(copyErr, closeErr, ctx.Err()); err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	if count != artifact.Size || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return pluginhost.ArtifactImportResult{}, serviceError("artifact_invalid", "source artifact contents changed")
	}
	manifestFile, err := root.Open(filepath.Join("sessions", request.SourceThreadID, "artifacts", artifact.ID, ".artifact.json"))
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(manifestFile, 1<<20))
	manifestFile.Close()
	if readErr != nil {
		return pluginhost.ArtifactImportResult{}, readErr
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	manifest["thread_id"] = request.TargetThreadID
	manifest["forked_from_thread_id"] = request.SourceThreadID
	raw, err = json.Marshal(manifest)
	if err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	if err := os.WriteFile(filepath.Join(temp, ".artifact.json"), raw, 0600); err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	if err := os.Rename(temp, target); err != nil {
		return pluginhost.ArtifactImportResult{}, err
	}
	artifact.URI = targetURI
	return artifact, nil
}

// ArtifactClient contains no local storage fallback. The callback must retain
// the caller's selected generation through invocation and dependent cleanup.
type ArtifactClient struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewArtifactClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *ArtifactClient {
	return &ArtifactClient{call: call}
}
func (c *ArtifactClient) Import(ctx context.Context, scope ArtifactScope, input pluginhost.ArtifactImportParams, present bool) (pluginhost.ArtifactImportResult, error) {
	var result pluginhost.ArtifactImportResult
	err := c.invoke(ctx, "import", ArtifactImportRequest{Scope: scope, Input: input, Present: present}, &result)
	return result, err
}
func (c *ArtifactClient) Resolve(ctx context.Context, threadID, uri string) (pluginhost.ArtifactImportResult, bool, error) {
	var result ArtifactResolveResult
	err := c.invoke(ctx, "resolve", ArtifactResolveRequest{ThreadID: threadID, URI: uri}, &result)
	return result.Artifact, result.Managed, err
}
func (c *ArtifactClient) Read(ctx context.Context, threadID, uri string, maxBytes int64) ([]byte, string, error) {
	var result ArtifactReadResult
	err := c.invoke(ctx, "read", ArtifactReadRequest{ArtifactResolveRequest: ArtifactResolveRequest{ThreadID: threadID, URI: uri}, MaxBytes: maxBytes}, &result)
	return result.Data, result.MIMEType, err
}
func (c *ArtifactClient) ReadRange(ctx context.Context, input ArtifactRangeRequest) (ArtifactRangeResult, error) {
	var result ArtifactRangeResult
	if !validArtifactRange(input) {
		return result, serviceError("invalid_params", "artifact range is invalid")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := c.invoke(ctx, "read-range", input, &result); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return ArtifactRangeResult{}, err
	}
	if result.Offset != input.Offset || result.Total < result.Offset || result.SHA256 != input.SHA256 || result.MIMEType == "" || int64(len(result.Data)) != min(input.MaxBytes, result.Total-result.Offset) {
		return ArtifactRangeResult{}, serviceError("invalid_response", "artifact provider returned a mismatched range")
	}
	return result, nil
}

func (c *ArtifactClient) Fork(ctx context.Context, sourceThreadID, targetThreadID, uri string) (pluginhost.ArtifactImportResult, error) {
	var result pluginhost.ArtifactImportResult
	err := c.invoke(ctx, "fork", ArtifactForkRequest{SourceThreadID: sourceThreadID, TargetThreadID: targetThreadID, URI: uri}, &result)
	return result, err
}

// DeleteThread is safe to retry explicitly after an uncertain response. The
// client never retries automatically or substitutes a local filesystem delete.
func (c *ArtifactClient) DeleteThread(ctx context.Context, threadID string) (ArtifactDeleteThreadResult, error) {
	if !validArtifactThreadID(threadID) {
		return ArtifactDeleteThreadResult{}, serviceError("invalid_params", "artifact thread id is invalid")
	}
	var result ArtifactDeleteThreadResult
	if err := c.invoke(ctx, "delete-thread", ArtifactDeleteThreadRequest{ThreadID: threadID}, &result); err != nil {
		var hostErr *pluginhost.HostServiceError
		code := "artifact_unavailable"
		if errors.As(err, &hostErr) {
			code = hostErr.Code
		}
		return ArtifactDeleteThreadResult{}, serviceError(code, "selected artifact cleanup failed; its outcome may be partial or unknown; retry the same thread")
	}
	if result.ThreadID != threadID || !result.Deleted {
		return ArtifactDeleteThreadResult{}, serviceError("invalid_response", "artifact cleanup outcome is unconfirmed; retry the same thread")
	}
	return result, nil
}

func (c *ArtifactClient) invoke(ctx context.Context, method string, input, output any) error {
	if c == nil || c.call == nil {
		return serviceError("service_unavailable", "artifact binding is unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	raw, err = c.call(ctx, method, raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return serviceError("invalid_response", "artifact provider returned an invalid response")
	}
	return nil
}

func artifactScope(scope pluginhost.ToolExecutionScope) ArtifactScope {
	return ArtifactScope{ThreadID: scope.ThreadID, ExecutionID: scope.ID, PluginID: scope.PluginID, CallID: scope.CallID, CWD: scope.CWD}
}

func (k *kernelHostServices) artifactClient(executionID string) *ArtifactClient {
	k.mu.RLock()
	registry := k.registry
	k.mu.RUnlock()
	return NewArtifactClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if registry == nil {
			return nil, serviceError("service_unavailable", "artifact service graph is unavailable")
		}
		result, err := registry.Call(ctx, k.ID(), pluginhost.ServiceCallParams{Service: ArtifactServiceName, Method: method, Params: raw, ExecutionID: executionID})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func validArtifactRange(input ArtifactRangeRequest) bool {
	digest, err := hex.DecodeString(input.SHA256)
	return validArtifactThreadID(input.ThreadID) && input.URI != "" && input.Offset >= 0 && input.MaxBytes > 0 && input.MaxBytes <= MaxArtifactRangeBytes && err == nil && len(digest) == sha256.Size
}

func (s *artifactService) readRange(ctx context.Context, input ArtifactRangeRequest) (ArtifactRangeResult, error) {
	if !validArtifactRange(input) {
		return ArtifactRangeResult{}, serviceError("invalid_params", "artifact range is invalid")
	}
	artifact, managed, err := resolveManagedArtifact(s.home, s.stateDir, input.ThreadID, input.URI)
	if err != nil {
		return ArtifactRangeResult{}, err
	}
	if !managed || artifact.SHA256 != input.SHA256 || input.Offset > artifact.Size {
		return ArtifactRangeResult{}, serviceError("artifact_invalid", "artifact range does not identify this snapshot")
	}
	root, err := os.OpenRoot(s.stateDir)
	if err != nil {
		return ArtifactRangeResult{}, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Join("sessions", input.ThreadID, "artifacts", artifact.ID, artifact.Name))
	if err != nil {
		return ArtifactRangeResult{}, err
	}
	defer file.Close()
	// Hash and collect the requested span in the same pass. No full-payload cache,
	// and a mutation between verification and a second read cannot change a range.
	hash := sha256.New()
	data := make([]byte, 0, min(input.MaxBytes, artifact.Size-input.Offset))
	buffer := make([]byte, 64*1024)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return ArtifactRangeResult{}, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
			start, end := max(offset, input.Offset), min(offset+int64(n), input.Offset+input.MaxBytes)
			if start < end {
				data = append(data, buffer[start-offset:end-offset]...)
			}
			offset += int64(n)
			if offset > artifact.Size {
				return ArtifactRangeResult{}, serviceError("artifact_invalid", "artifact size changed")
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return ArtifactRangeResult{}, readErr
		}
	}
	if offset != artifact.Size || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return ArtifactRangeResult{}, serviceError("artifact_invalid", "artifact contents changed")
	}
	return ArtifactRangeResult{ArtifactReadResult: ArtifactReadResult{Data: data, MIMEType: artifact.MIMEType}, Offset: input.Offset, Total: artifact.Size, SHA256: artifact.SHA256}, nil
}
