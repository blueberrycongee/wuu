package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

const (
	capabilityPrompt    = "agent.system_prompt.section"
	capabilityClient    = "plugin.client.request"
	capabilityLifecycle = "agent.turn.lifecycle"
	maxMemoryFileBytes  = 256 * 1024
)

type controller struct {
	mu                sync.Mutex
	host              pluginapi.Host
	notebook          string
	workspaceStateDir string
	jobs              map[string]*job
}

type job struct {
	ID        string               `json:"id"`
	Kind      string               `json:"kind"`
	RequestID string               `json:"request_id"`
	SessionID string               `json:"session_id"`
	State     string               `json:"state"`
	Output    string               `json:"output,omitempty"`
	Error     string               `json:"error,omitempty"`
	Changed   []changedFile        `json:"changed_files,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	before    map[string]fileStamp `json:"-"`
}

type changedFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

type fileStamp struct {
	Size    int64
	ModTime int64
}

type fileInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"`
}

func Handler() pluginapi.Handler {
	c := &controller{jobs: make(map[string]*job)}
	return pluginapi.Handler{
		Definition: pluginapi.Definition{
			Tools: []pluginapi.Tool{
				{ID: "memory_list", Description: "List the user's durable memory notebook files. Use only when durable cross-project user preferences, feedback, references, or lessons are relevant.", InputSchema: emptySchema(), Activity: &pluginapi.ToolActivity{ReadOnly: true, ConcurrencySafe: true, Risk: "low"}},
				{ID: "memory_read", Description: "Read one Markdown file from the user's durable memory notebook. MEMORY.md is the index; topic files contain the actual memories.", InputSchema: objectSchema(map[string]any{"file": stringField("Notebook filename such as MEMORY.md or feedback_testing.md.")}, "file"), Activity: &pluginapi.ToolActivity{ReadOnly: true, ConcurrencySafe: true, Risk: "low"}},
				{ID: "memory_write", Description: "Create or replace one Markdown file in the user's durable memory notebook. Saving a memory requires updating both its topic file and the MEMORY.md index.", InputSchema: objectSchema(map[string]any{"file": stringField("Notebook Markdown filename."), "content": stringField("Complete replacement Markdown content.")}, "file", "content")},
				{ID: "memory_delete", Description: "Delete one topic file from the user's durable memory notebook. Also remove its pointer from MEMORY.md. MEMORY.md itself cannot be deleted.", InputSchema: objectSchema(map[string]any{"file": stringField("Topic Markdown filename to delete.")}, "file")},
				{ID: "session_memory", Description: "Read or update workspace and current-session memory. Use project_memory only for stable workspace facts; summary for compact recoverable task state; checkpoint only for legacy compatibility; notes for session scratch notes.", InputSchema: sessionMemorySchema()},
			},
			Capabilities: []pluginapi.Capability{
				{ID: capabilityPrompt, Kind: "transform", Version: 1},
				{ID: capabilityClient, Kind: "decision", Version: 1},
				{ID: capabilityLifecycle, Kind: "observe", Version: 1, ErrorPolicy: "isolate"},
			},
			RequiredHostServices: []pluginapi.HostService{
				{ID: pluginapi.HostServiceSessionCreate, Required: true},
				{ID: pluginapi.HostServiceSessionSend, Required: true},
			},
			ProvidedServices: []pluginapi.Service{sessionMemoryServiceDescriptor(), notebookMemoryServiceDescriptor()},
		},
		Initialize:       c.initialize,
		Activate:         c.activate,
		ExecuteTool:      c.executeTool,
		InvokeCapability: c.invokeCapability,
		InvokeService:    c.invokeService,
	}
}

func (c *controller) initialize(_ context.Context, host pluginapi.Host, params pluginapi.InitializeParams) error {
	if host == nil {
		return errors.New("memory host is required")
	}
	notebook := userNotebook(params.WuuHome)
	if notebook == "" {
		return errors.New("memory plugin requires wuu_home")
	}
	c.mu.Lock()
	c.host = host
	c.notebook = notebook
	c.workspaceStateDir = strings.TrimSpace(params.WorkspaceStateDir)
	c.mu.Unlock()
	return nil
}

func (c *controller) activate(context.Context) error {
	return ensureNotebook(c.notebookPath())
}

func (c *controller) executeTool(_ context.Context, _ pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	switch call.ToolID {
	case "session_memory":
		return c.executeSessionMemory(call)
	case "memory_list":
		files, err := c.readNotebook()
		return jsonResult(map[string]any{"files": files}, err)
	case "memory_read":
		var input struct {
			File string `json:"file"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return pluginapi.ToolResult{}, err
		}
		content, err := c.readFile(input.File)
		return jsonResult(map[string]any{"file": input.File, "content": content}, err)
	case "memory_write":
		var input struct {
			File    string `json:"file"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return pluginapi.ToolResult{}, err
		}
		if err := c.writeFile(input.File, input.Content); err != nil {
			return pluginapi.ToolResult{}, err
		}
		return jsonResult(map[string]any{"file": input.File, "written": true}, nil)
	case "memory_delete":
		var input struct {
			File string `json:"file"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return pluginapi.ToolResult{}, err
		}
		if err := c.deleteFile(input.File); err != nil {
			return pluginapi.ToolResult{}, err
		}
		return jsonResult(map[string]any{"file": input.File, "deleted": true}, nil)
	default:
		return pluginapi.ToolResult{}, fmt.Errorf("unknown memory tool %q", call.ToolID)
	}
}

func (c *controller) invokeCapability(ctx context.Context, _ pluginapi.Host, call pluginapi.CapabilityCall) (json.RawMessage, error) {
	switch call.Capability {
	case capabilityPrompt:
		raw, err := os.ReadFile(filepath.Join(c.notebookPath(), indexFileName))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("memory: read index: %w", err)
		}
		index, truncated := safeIndex(string(raw), maxIndexBytes)
		if truncated {
			index += "\n\n> WARNING: MEMORY.md was truncated before prompt injection. Keep index entries short and move detail into topic files."
		}
		return json.Marshal(map[string]string{"text": promptSection(c.notebookPath(), index)})
	case capabilityClient:
		var request struct {
			Method string          `json:"method"`
			Input  json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(call.Input, &request); err != nil {
			return nil, err
		}
		value, err := c.clientRequest(ctx, request.Method, request.Input)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]json.RawMessage{"result": encoded})
	case capabilityLifecycle:
		var input pluginapi.TurnLifecycleInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return nil, err
		}
		c.settle(input)
		return json.RawMessage(`{}`), nil
	default:
		return nil, fmt.Errorf("unknown memory capability %q", call.Capability)
	}
}

func (c *controller) clientRequest(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch strings.TrimSpace(method) {
	case "memory.read":
		files, err := c.readNotebook()
		if err != nil {
			return nil, err
		}
		index, err := c.readFileOptional(indexFileName)
		return map[string]any{"index_raw": index, "files": files}, err
	case "memory.job.get":
		var input struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return c.getJob(input.ID)
	case "memory.overview.start":
		return c.startJob(ctx, "overview", overviewPrompt())
	case "memory.chat.start":
		var input struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.Message) == "" {
			return nil, errors.New("memory chat message is required")
		}
		return c.startJob(ctx, "chat", managerPrompt(input.Message))
	default:
		return nil, fmt.Errorf("unknown memory client method %q", method)
	}
}

func (c *controller) startJob(ctx context.Context, kind, prompt string) (job, error) {
	id, err := randomID("memory")
	if err != nil {
		return job{}, err
	}
	requestID := id + ":turn"
	var created pluginapi.SessionCreateResult
	err = c.host.CallHost(ctx, pluginapi.HostServiceSessionCreate, pluginapi.SessionCreateParams{
		RequestID: id + ":session", Name: "Memory " + kind, Visibility: "plugin", ContextSource: "fresh", Workspace: "shared",
	}, &created)
	if err != nil {
		return job{}, err
	}
	entry := &job{ID: id, Kind: kind, RequestID: requestID, SessionID: created.SessionID, State: "starting", CreatedAt: time.Now().UTC()}
	if kind == "chat" {
		entry.before = c.snapshotFiles()
	}
	c.mu.Lock()
	c.jobs[id] = entry
	c.mu.Unlock()
	var sent pluginapi.SessionSendResult
	err = c.host.CallHost(ctx, pluginapi.HostServiceSessionSend, pluginapi.SessionSendParams{
		RequestID: requestID,
		SessionID: created.SessionID,
		Input:     pluginapi.SessionInput{Prompt: prompt},
		Cause:     "memory." + kind,
	}, &sent)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry = c.jobs[id]
	if err != nil {
		entry.State = "failed"
		entry.Error = err.Error()
		return *entry, err
	}
	entry.State = sent.State
	return *entry, nil
}

func (c *controller) settle(input pluginapi.TurnLifecycleInput) {
	if input.State != "completed" && input.State != "failed" && input.State != "interrupted" && input.State != "discarded" {
		return
	}
	c.mu.Lock()
	for _, entry := range c.jobs {
		if entry.RequestID != input.RequestID {
			continue
		}
		entry.State = input.State
		entry.Output = strings.TrimSpace(input.FinalOutput)
		entry.Error = strings.TrimSpace(input.Error)
		kind := entry.Kind
		before := entry.before
		id := entry.ID
		c.mu.Unlock()
		if kind == "chat" {
			changed := diffFiles(before, c.snapshotFiles())
			c.mu.Lock()
			if current := c.jobs[id]; current != nil {
				current.Changed = changed
			}
			c.mu.Unlock()
		}
		return
	}
	c.mu.Unlock()
}

func (c *controller) getJob(id string) (job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.jobs[strings.TrimSpace(id)]
	if entry == nil {
		return job{}, fmt.Errorf("memory job %q not found", id)
	}
	return *entry, nil
}

func (c *controller) notebookPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.notebook
}

func (c *controller) safePath(name string, allowIndex bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name != filepath.Base(name) || filepath.Ext(name) != ".md" {
		return "", errors.New("memory file must be a plain .md filename")
	}
	if !allowIndex && strings.EqualFold(name, indexFileName) {
		return "", errors.New("MEMORY.md cannot be deleted")
	}
	return filepath.Join(c.notebookPath(), name), nil
}

func (c *controller) readFile(name string) (string, error) {
	path, err := c.safePath(name, true)
	if err != nil {
		return "", err
	}
	raw, err := readNotebookFile(filepath.Dir(path), filepath.Base(path))
	return string(raw), err
}

func (c *controller) readFileOptional(name string) (string, error) {
	value, err := c.readFile(name)
	if os.IsNotExist(err) {
		return "", nil
	}
	return value, err
}

func (c *controller) writeFile(name, content string) error {
	if len(content) > maxMemoryFileBytes {
		return fmt.Errorf("memory content exceeds %d bytes", maxMemoryFileBytes)
	}
	path, err := c.safePath(name, true)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.notebookPath(), ".memory-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (c *controller) deleteFile(name string) error {
	path, err := c.safePath(name, false)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (c *controller) readNotebook() ([]fileInfo, error) {
	entries, err := os.ReadDir(c.notebookPath())
	if err != nil {
		return nil, err
	}
	files := make([]fileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || strings.EqualFold(entry.Name(), indexFileName) {
			continue
		}
		content, err := c.readFile(entry.Name())
		if err != nil {
			return nil, err
		}
		memoryType, description := frontmatter(content)
		files = append(files, fileInfo{Name: entry.Name(), Type: memoryType, Description: description, Content: content})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func (c *controller) snapshotFiles() map[string]fileStamp {
	out := make(map[string]fileStamp)
	entries, _ := os.ReadDir(c.notebookPath())
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		if info, err := entry.Info(); err == nil {
			out[entry.Name()] = fileStamp{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		}
	}
	return out
}

func diffFiles(before, after map[string]fileStamp) []changedFile {
	var out []changedFile
	for path, current := range after {
		previous, ok := before[path]
		action := "created"
		if ok {
			if previous == current {
				continue
			}
			action = "modified"
		}
		out = append(out, changedFile{Path: path, Action: action})
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			out = append(out, changedFile{Path: path, Action: "deleted"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func frontmatter(content string) (string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	var memoryType, description string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "type":
			memoryType = strings.TrimSpace(value)
		case "description":
			description = strings.TrimSpace(value)
		}
	}
	return memoryType, description
}

func promptSection(notebook, index string) string {
	if strings.TrimSpace(index) == "" {
		index = "The MEMORY.md index is currently empty."
	}
	return strings.Join([]string{
		"# Durable user memory",
		"The memory plugin owns a persistent notebook at `" + notebook + "`. Use memory_list and memory_read to inspect it, and memory_write/memory_delete to maintain it.",
		"The plugin also provides session_memory for workspace project_memory and current-session summary, checkpoint, and notes files. Read project_memory on demand; do not copy it into every prompt.",
		"Save only durable cross-project user preferences, explicit feedback, stable references, and reusable lessons. Never save secrets, raw transcripts, task progress, commit ids, or facts derivable from the current repository.",
		"Each memory belongs in a topic .md file with frontmatter fields name, description, and type (user | feedback | reference | lesson). MEMORY.md is only a one-line-per-topic index. A save or forget operation must update both the topic file and MEMORY.md.",
		"If remembered information conflicts with current evidence, trust current evidence.",
		"\n## MEMORY.md index\n\n" + index,
	}, "\n\n")
}

func overviewPrompt() string {
	return "Read the user's memory notebook with memory_list and memory_read. Produce a concise Markdown overview of what is actually recorded, grouped by useful themes. Do not modify memory. Clearly say when the notebook is empty."
}

func managerPrompt(message string) string {
	return "Manage the user's durable memory according to this request:\n\n" + strings.TrimSpace(message) + "\n\nInspect existing files first. Use memory_write and memory_delete as needed, keeping MEMORY.md as a one-line index rather than a content file. Do not save secrets, raw transcripts, temporary task state, commit ids, or repository-derived facts. Finish with a concise explanation of exactly what changed."
}

func randomID(prefix string) (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(raw[:]), nil
}

func jsonResult(value any, err error) (pluginapi.ToolResult, error) {
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(string(raw)), nil
}

func emptySchema() map[string]any { return objectSchema(nil) }

func stringField(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
