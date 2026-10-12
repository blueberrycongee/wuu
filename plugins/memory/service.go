package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

// NotebookMemoryService exposes bounded, read-only user notebook snapshots.
const NotebookMemoryService = "memory.notebook"

const (
	defaultTopicBytes = 16 * 1024
	maxTopicBytes     = 64 * 1024
)

// SessionMemoryService is the stable registry name other plugins resolve to
// share session memory semantics instead of touching the files directly.
const SessionMemoryService = "memory.session"

func notebookMemoryServiceDescriptor() pluginapi.Service {
	return pluginapi.Service{
		Name:    NotebookMemoryService,
		Version: "1.0.0",
		Methods: []pluginapi.ServiceMethod{
			{Name: "index", InputSchema: "memory.notebook.index.request.v1", OutputSchema: "memory.notebook.snapshot.v1"},
			{Name: "read", InputSchema: "memory.notebook.read.request.v1", OutputSchema: "memory.notebook.snapshot.v1"},
		},
	}
}

type notebookReadParams struct {
	File     string `json:"file,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// NotebookSource identifies the original user notebook file, not a model summary.
type NotebookSource struct {
	Scope string `json:"scope"`
	File  string `json:"file"`
}

// NotebookSnapshot is one fresh file read. Revision identifies the complete
// source content, independent of the returned excerpt. A missing file has no
// revision; an existing empty file does. Consumers must reread before reuse.
type NotebookSnapshot struct {
	Version   int            `json:"version"`
	Source    NotebookSource `json:"source"`
	Found     bool           `json:"found"`
	Revision  string         `json:"revision,omitempty"`
	Content   string         `json:"content"`
	Truncated bool           `json:"truncated"`
}

func (c *controller) readNotebookSnapshot(method string, params notebookReadParams) (NotebookSnapshot, error) {
	name := strings.TrimSpace(params.File)
	limit, maxBytes := defaultTopicBytes, maxTopicBytes
	switch method {
	case "index":
		if name != "" {
			return NotebookSnapshot{}, fmt.Errorf("memory.notebook index does not accept a file")
		}
		name = indexFileName
		limit, maxBytes = maxIndexBytes, maxIndexBytes
	case "read":
		if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) || filepath.Ext(name) != ".md" || strings.EqualFold(name, indexFileName) {
			return NotebookSnapshot{}, fmt.Errorf("memory.notebook read requires a plain, non-hidden topic .md filename; use index for MEMORY.md")
		}
	default:
		return NotebookSnapshot{}, fmt.Errorf("unknown memory.notebook method %q", method)
	}
	if params.MaxBytes < 0 || params.MaxBytes > maxBytes {
		return NotebookSnapshot{}, fmt.Errorf("memory.notebook max_bytes must be between 1 and %d, or 0 for the default", maxBytes)
	}
	if params.MaxBytes != 0 {
		limit = params.MaxBytes
	}
	result := NotebookSnapshot{Version: 1, Source: NotebookSource{Scope: "user_notebook", File: name}}
	raw, err := readNotebookFile(c.notebookPath(), name)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return NotebookSnapshot{}, err
	}
	result.Found = true
	result.Revision = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if method == "index" {
		result.Content, result.Truncated = safeIndex(string(raw), limit)
	} else {
		result.Content, result.Truncated = boundedNotebookText(string(raw), limit)
	}
	return result, nil
}

func sessionMemoryServiceDescriptor() pluginapi.Service {
	return pluginapi.Service{
		Name:    SessionMemoryService,
		Version: "1.0.0",
		Methods: []pluginapi.ServiceMethod{
			{Name: "status", InputSchema: "memory.session.status.request.v1", OutputSchema: "memory.session.status.response.v1"},
			{Name: "read", InputSchema: "memory.session.read.request.v1", OutputSchema: "memory.session.read.response.v1"},
			{Name: "append", InputSchema: "memory.session.append.request.v1", OutputSchema: "memory.session.append.response.v1"},
			{Name: "replace", InputSchema: "memory.session.replace.request.v1", OutputSchema: "memory.session.replace.response.v1"},
		},
	}
}

// sessionMemoryServiceParams carries one memory.session call. thread_id is
// required for the session-scoped targets (summary, checkpoint, notes) and
// optional for the workspace-scoped project_memory target.
type sessionMemoryServiceParams struct {
	Target   string `json:"target,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	Content  string `json:"content,omitempty"`
	Source   string `json:"source,omitempty"`
}

func (c *controller) invokeService(_ context.Context, _ pluginapi.Host, call pluginapi.ServiceCall) (json.RawMessage, error) {
	if call.Service == NotebookMemoryService {
		var params notebookReadParams
		if len(call.Params) != 0 {
			if err := json.Unmarshal(call.Params, &params); err != nil {
				return nil, fmt.Errorf("memory.notebook params: %w", err)
			}
		}
		result, err := c.readNotebookSnapshot(call.Method, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if call.Service != SessionMemoryService {
		return nil, fmt.Errorf("memory plugin does not provide service %q", call.Service)
	}
	var params sessionMemoryServiceParams
	if len(call.Params) != 0 {
		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, fmt.Errorf("memory.session params: %w", err)
		}
	}
	source := strings.TrimSpace(params.Source)
	if source == "" {
		source = strings.TrimSpace(call.Caller)
	}
	result, err := c.runSessionMemory(sessionMemoryArgs{
		Action:  call.Method,
		Target:  params.Target,
		Content: params.Content,
		Source:  source,
	}, params.ThreadID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
