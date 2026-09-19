package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
)

const toolNotes = "notes"
const maxNotesBytes = 1_000_000

type NotesTool struct{ env *Env }

func NewNotesTool(env *Env) *NotesTool     { return &NotesTool{env: env} }
func (*NotesTool) Name() string            { return toolNotes }
func (*NotesTool) IsReadOnly() bool        { return false }
func (*NotesTool) IsConcurrencySafe() bool { return true }

func (*NotesTool) Classify(arguments string) ToolClassification {
	var input notesArguments
	_ = json.Unmarshal([]byte(arguments), &input)
	readOnly := input.Action == "list" || input.Action == "read" || input.Action == "search"
	return ToolClassification{ReadOnly: readOnly, ConcurrencySafe: true, Risk: ToolRiskLow, Reason: "session working notes"}
}

func (*NotesTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        toolNotes,
		Description: "Maintain persistent working notes for this session. Save objectives, constraints, decisions, progress, checks and next steps as work proceeds and before new_context. Read notes after a context switch; use history_read/history_search for exact facts. These virtual files survive resets, restarts and model changes; they do not write workspace files. Actions: list, read, search (literal substring), write (replace), append. Paths are relative. Reads/search use Unicode character offsets and bounded pages. Writes require the revision returned by list/read/search (use the empty revision for a new collection); conflicts require rereading. Total stored JSON is limited to 1 MB per session. No background model maintains these notes.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"action"},
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"list", "read", "search", "write", "append"}},
				"path":     map[string]any{"type": "string", "description": "Virtual note path, required for read/write/append; prefix for list/search."},
				"content":  map[string]any{"type": "string", "description": "Text for write or append."},
				"query":    map[string]any{"type": "string", "description": "Literal case-sensitive search text."},
				"revision": map[string]any{"type": "string", "description": "Collection revision. Required for mutations; optional on reads to reject stale pagination."},
				"offset":   map[string]any{"type": "integer", "minimum": 0, "description": "Character offset for read; item offset for list/search."},
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 16000, "description": "Read characters (default 8000) or list/search items (default 20, max 100)."},
			},
		},
	}
}

type notesArguments struct {
	Action   string  `json:"action"`
	Path     string  `json:"path"`
	Content  string  `json:"content"`
	Query    string  `json:"query"`
	Revision *string `json:"revision"`
	Offset   int     `json:"offset"`
	Limit    int     `json:"limit"`
}

func notesRevision(value *string) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(*value)))
}

func validNotePath(path string) bool {
	if path == "" || len(path) > 240 || strings.ContainsAny(path, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (t *NotesTool) Execute(ctx context.Context, arguments string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.env == nil || strings.TrimSpace(t.env.SessionID) == "" {
		return "", errors.New("notes require a session")
	}
	var input notesArguments
	if err := decodeArgs(arguments, &input); err != nil {
		return "", err
	}
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 16000 {
		return "", errors.New("invalid notes page bounds")
	}
	home := t.env.WorkingNotesHome
	if home == "" {
		var err error
		home, err = statepath.Home("")
		if err != nil {
			return "", err
		}
	}
	store := workingnotes.Store{Home: home}
	stored, err := store.Read(t.env.SessionID)
	if err != nil {
		return "", err
	}
	files := map[string]string{}
	if stored != nil {
		if err := json.Unmarshal([]byte(*stored), &files); err != nil || files == nil {
			return "", errors.New("stored notes are invalid; refusing to overwrite")
		}
	}
	revision := notesRevision(stored)
	if input.Revision != nil && *input.Revision != revision {
		return "", errors.New("notes revision changed; reread before retrying")
	}
	output := map[string]any{"revision": revision}
	switch input.Action {
	case "write", "append":
		if input.Revision == nil {
			return "", errors.New("write and append require a revision from a prior read or list")
		}
		if !validNotePath(input.Path) || !utf8.ValidString(input.Content) {
			return "", errors.New("invalid note path or UTF-8 content")
		}
		content := input.Content
		if input.Action == "append" {
			content = files[input.Path] + content
		}
		files[input.Path] = content
		encoded, err := json.Marshal(files)
		if err != nil {
			return "", err
		}
		if len(encoded) > maxNotesBytes {
			return "", errors.New("notes exceed the 1 MB session limit; shorten existing notes")
		}
		value := string(encoded)
		if err := store.CompareAndSwap(t.env.SessionID, stored, value); err != nil {
			return "", err
		}
		output["revision"], output["path"], output["bytes"] = notesRevision(&value), input.Path, len(content)
	case "read":
		content, found := files[input.Path]
		if !found {
			return "", errors.New("note not found; use list to discover paths")
		}
		chars := []rune(content)
		limit := input.Limit
		if limit == 0 {
			limit = 8000
		}
		start := min(input.Offset, len(chars))
		end := min(start+limit, len(chars))
		// Bound serialized bytes as well as characters, including JSON escaping.
		for end > start {
			encoded, _ := json.Marshal(string(chars[start:end]))
			if len(encoded) <= 16000 {
				break
			}
			end = start + (end-start)/2
		}
		output["path"], output["content"], output["offset"] = input.Path, string(chars[start:end]), start
		output["total_characters"] = len(chars)
		if end < len(chars) {
			output["next_offset"] = end
		}
	case "list", "search":
		if input.Action == "search" && input.Query == "" {
			return "", errors.New("search requires a nonempty query")
		}
		paths := make([]string, 0, len(files))
		for path := range files {
			if strings.HasPrefix(path, input.Path) {
				paths = append(paths, path)
			}
		}
		sort.Strings(paths)
		items := []map[string]any{}
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		limit = min(limit, 100)
		total := 0
		pageBytes, pageFull := 0, false
		collect := func(item map[string]any) {
			if total >= input.Offset && len(items) < limit && !pageFull {
				encoded, _ := json.Marshal(item)
				if pageBytes+len(encoded) > 16000 {
					pageFull = true
				} else {
					items = append(items, item)
					pageBytes += len(encoded)
				}
			}
			total++
		}
		for _, path := range paths {
			if input.Action == "list" {
				collect(map[string]any{"path": path, "bytes": len(files[path])})
				continue
			}
			text := files[path]
			charOffset := 0
			for start := 0; start < len(text); {
				index := strings.Index(text[start:], input.Query)
				if index < 0 {
					break
				}
				index += start
				charOffset += utf8.RuneCountInString(text[start:index])
				if total >= input.Offset && len(items) < limit {
					end := index
					for count := 0; end < len(text) && count < 160; count++ {
						_, size := utf8.DecodeRuneInString(text[end:])
						end += size
					}
					collect(map[string]any{"path": path, "offset": charOffset, "excerpt": text[index:end]})
				} else {
					total++
				}
				charOffset += utf8.RuneCountInString(input.Query)
				start = index + len(input.Query)
			}
		}
		end := min(input.Offset, total) + len(items)
		output["items"], output["total"] = items, total
		if end < total {
			output["next_offset"] = end
		}
	default:
		return "", errors.New("unknown notes action")
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
