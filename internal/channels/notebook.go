package channels

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

const notebookTopicLimit = 64 * 1024

type NotebookParams struct {
	Action   string `json:"action"`
	Scope    string `json:"scope"`
	RoomID   string `json:"room_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Query    string `json:"query,omitempty"`
	After    string `json:"after,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Content  string `json:"content,omitempty"`
	Revision string `json:"revision,omitempty"`
}
type NotebookEntry struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
	Content  string `json:"content,omitempty"`
}
type NotebookResult struct {
	Entries []NotebookEntry `json:"entries"`
	Next    string          `json:"next,omitempty"`
}

// Notebook keeps identity-private and room-shared knowledge in their existing
// markdown directories. Revisions prevent sibling sessions from losing edits.
func (c *AgentClient) Notebook(ctx context.Context, p NotebookParams) (NotebookResult, error) {
	actor, err := c.service.AuthenticatePrincipal(ctx, c.agentID, c.token)
	if err != nil {
		return NotebookResult{}, err
	}
	if c.sessionRef == "" {
		return NotebookResult{}, ErrUnauthorized
	}
	b, err := c.GetCollaborationSession(ctx, c.sessionRef)
	if err != nil {
		return NotebookResult{}, err
	}
	if p.RoomID == "" {
		p.RoomID = b.RoomID
	}
	if p.RoomID != b.RoomID {
		return NotebookResult{}, ErrUnauthorized
	}
	if err = c.service.requireRoomPrincipalAccess(ctx, p.RoomID, actor.ID); err != nil {
		return NotebookResult{}, err
	}
	dir := actor.MemoryDir
	switch p.Scope {
	case "", "identity":
		if actor.IsRoomRuntime() {
			return NotebookResult{}, errors.New("coordinators use room memory")
		}
	case "room":
		dir, err = c.service.roomNotebookDir(ctx, p.RoomID)
		if err != nil {
			return NotebookResult{}, err
		}
	default:
		return NotebookResult{}, errors.New("scope must be identity or room")
	}
	c.service.mu.Lock()
	defer c.service.mu.Unlock()
	if p.Action == "write" || p.Action == "delete" {
		tx, e := c.service.db.BeginTx(ctx, nil)
		if e != nil {
			return NotebookResult{}, e
		}
		e = validateCollaborationSessionWriteTx(ctx, tx, c.sessionRef, actor.ID, p.RoomID, "", 0)
		_ = tx.Rollback()
		if e != nil {
			return NotebookResult{}, e
		}
	}
	return operateNotebook(dir, p)
}

// RoomNotebook is the trusted user's inspection and correction path.
func (s *Service) RoomNotebook(ctx context.Context, roomID, ownerID string, p NotebookParams) (NotebookResult, error) {
	dir, err := s.roomNotebookDir(ctx, roomID)
	if err != nil {
		return NotebookResult{}, err
	}
	if ownerID != "" {
		if err = s.requireRoomPrincipalAccess(ctx, roomID, ownerID); err != nil {
			return NotebookResult{}, err
		}
		actor, e := s.GetNamedAgent(ctx, ownerID)
		if e != nil {
			return NotebookResult{}, e
		}
		dir = actor.MemoryDir
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return operateNotebook(dir, p)
}

func (s *Service) roomNotebookDir(ctx context.Context, roomID string) (string, error) {
	room, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return "", err
	}
	if room.RuntimeID != "" {
		runtime, e := s.GetRoomRuntime(ctx, room.RuntimeID)
		return runtime.MemoryDir, e
	}
	dir := filepath.Join(s.dir, "rooms", room.ID, "memory")
	return dir, securefs.Mkdir(dir)
}

func operateNotebook(dir string, p NotebookParams) (NotebookResult, error) {
	result := NotebookResult{Entries: []NotebookEntry{}}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result, err
	}
	defer root.Close()
	read := func(name string) (NotebookEntry, error) {
		entry := NotebookEntry{Name: name}
		f, e := root.Open(name)
		if os.IsNotExist(e) {
			entry.Revision = "missing"
			return entry, nil
		}
		if e != nil {
			return entry, e
		}
		defer f.Close()
		data, e := io.ReadAll(io.LimitReader(f, notebookTopicLimit+1))
		if e != nil {
			return entry, e
		}
		if len(data) > notebookTopicLimit {
			return entry, errors.New("memory topic exceeds 64 KiB; use file tools to split it")
		}
		entry.Content = string(data)
		entry.Revision = fmt.Sprintf("%x", sha256.Sum256(data))
		return entry, nil
	}
	valid := func(name string) bool {
		return name != "" && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\") && strings.HasSuffix(name, ".md")
	}
	if p.Action == "list" || p.Action == "search" {
		entries, e := os.ReadDir(dir)
		if e != nil {
			return result, e
		}
		limit := p.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		for _, entry := range entries {
			if entry.Name() <= p.After || !valid(entry.Name()) || !entry.Type().IsRegular() {
				continue
			}
			item, e := read(entry.Name())
			if e != nil {
				return result, e
			}
			if p.Query != "" && !strings.Contains(strings.ToLower(item.Name+"\n"+item.Content), strings.ToLower(p.Query)) {
				continue
			}
			if len(result.Entries) == limit {
				result.Next = result.Entries[len(result.Entries)-1].Name
				break
			}
			lines := strings.SplitN(item.Content, "\n", 5)
			item.Content = strings.Join(lines[:min(len(lines), 4)], "\n")
			if runes := []rune(item.Content); len(runes) > 400 {
				item.Content = string(runes[:400])
			}
			result.Entries = append(result.Entries, item)
		}
		return result, nil
	}
	if !valid(p.Name) {
		return result, errors.New("memory name must be a single markdown filename")
	}
	item, err := read(p.Name)
	if err != nil {
		return result, err
	}
	switch p.Action {
	case "read":
	case "write", "delete":
		if p.Revision == "" || p.Revision != item.Revision {
			return result, fmt.Errorf("%w: read the memory first and supply its revision (missing for a new topic)", ErrConflict)
		}
		if p.Action == "write" {
			if len(p.Content) > notebookTopicLimit {
				return result, errors.New("memory topic must be at most 64 KiB")
			}
			// The resolved root is owned by Collaboration; reject links before the
			// atomic replacement so no notebook operation follows external paths.
			if info, e := root.Lstat(p.Name); e == nil && !info.Mode().IsRegular() {
				return result, errors.New("memory topic must be a regular file")
			}
		}
		if p.Name != "MEMORY.md" {
			index, e := read("MEMORY.md")
			if e != nil {
				return result, e
			}
			lines := strings.Split(index.Content, "\n")
			kept := make([]string, 0, len(lines)+1)
			target := "(" + p.Name + ")"
			for _, line := range lines {
				if !strings.Contains(line, target) {
					kept = append(kept, line)
				}
			}
			if p.Action == "write" {
				kept = append(kept, "- ["+p.Name+"]("+p.Name+")")
			}
			// Topic corrections must not leave an obsolete preference in the injected index.
			if e = securefs.WriteFileAtomic(filepath.Join(dir, "MEMORY.md"), []byte(strings.TrimSpace(strings.Join(kept, "\n"))+"\n")); e != nil {
				return result, e
			}
		}
		if p.Action == "delete" {
			if item.Revision != "missing" {
				err = root.Remove(p.Name)
			}
			item.Content = ""
			item.Revision = "missing"
		} else {
			err = securefs.WriteFileAtomic(filepath.Join(dir, p.Name), []byte(p.Content))
			item.Content = p.Content
			item.Revision = fmt.Sprintf("%x", sha256.Sum256([]byte(p.Content)))
		}
	default:
		return result, errors.New("action must be list, search, read, write or delete")
	}
	if err != nil {
		return result, err
	}
	result.Entries = append(result.Entries, item)
	return result, nil
}
