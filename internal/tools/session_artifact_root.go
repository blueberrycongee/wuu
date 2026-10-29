package tools

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// sessionArtifactRoot is owned by the session and shared with its toolkits.
// All filesystem access occurs relative to the pinned directory handle.
type sessionArtifactRoot struct {
	root      *os.Root
	lifecycle sync.RWMutex
	closed    bool
}

type SessionArtifactManager struct {
	mu    sync.Mutex
	roots map[string]*sessionArtifactEntry
}
type sessionArtifactEntry struct {
	root *sessionArtifactRoot
	refs int
}
type SessionArtifactLease struct {
	manager *SessionArtifactManager
	key     string
	root    *sessionArtifactRoot
	once    sync.Once
	err     error
}

func NewSessionArtifactManager() *SessionArtifactManager {
	return &SessionArtifactManager{roots: make(map[string]*sessionArtifactEntry)}
}

func (m *SessionArtifactManager) acquire(path string) (*SessionArtifactLease, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty session artifact path")
	}
	key, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.roots[key]
	if entry == nil {
		root, err := newSessionArtifactRoot(key)
		if err != nil {
			return nil, err
		}
		entry = &sessionArtifactEntry{root: root}
		m.roots[key] = entry
	}
	entry.refs++
	return &SessionArtifactLease{manager: m, key: key, root: entry.root}, nil
}

func (l *SessionArtifactLease) Close() error {
	l.once.Do(func() {
		l.manager.mu.Lock()
		defer l.manager.mu.Unlock()
		entry := l.manager.roots[l.key]
		entry.refs--
		if entry.refs == 0 {
			delete(l.manager.roots, l.key)
			l.err = entry.root.Close()
		}
	})
	return l.err
}

func newSessionArtifactRoot(path string) (*sessionArtifactRoot, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty session artifact path")
	}
	parentPath, name := filepath.Dir(path), filepath.Base(path)
	if err := securefs.Mkdir(parentPath); err != nil {
		return nil, err
	}
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err := parent.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("session artifact root is not a directory")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, errors.New("session artifact root changed during binding")
	}
	return &sessionArtifactRoot{root: root}, nil
}

func (s *sessionArtifactRoot) Close() error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.root.Close()
}

// openManaged confines managed reads to the pinned session root. Root permits
// relative links inside that root while rejecting links that escape it.
func (s *sessionArtifactRoot) openManaged(name string) (*os.File, error) {
	f, err := s.root.OpenFile(name, os.O_RDONLY|artifactNonblockFlag, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("session artifact is not a regular file")
	}
	return f, nil
}

func (s *sessionArtifactRoot) open(name string) (*os.File, error) {
	if filepath.Clean(filepath.Dir(name)) != "tool-results" {
		return nil, errors.New("invalid archive path")
	}
	dir, err := s.openArchiveDir(false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	name = filepath.Base(name)
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("session artifact is not a regular file")
	}
	f, err := dir.OpenFile(name, os.O_RDONLY|artifactNonblockFlag, 0)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		f.Close()
		return nil, errors.New("session artifact changed during open")
	}
	return f, nil
}

func (s *sessionArtifactRoot) openArchiveDir(create bool) (*os.Root, error) {
	if create {
		if err := s.root.Mkdir("tool-results", 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := s.root.Lstat("tool-results")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("archive directory is not a directory")
	}
	dir, err := s.root.OpenRoot("tool-results")
	if err != nil {
		return nil, err
	}
	actual, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		dir.Close()
		return nil, errors.New("archive directory changed during open")
	}
	return dir, nil
}

func (s *sessionArtifactRoot) writeArchive(name string, data []byte) error {
	if name != filepath.Base(name) {
		return errors.New("invalid archive name")
	}
	dir, err := s.openArchiveDir(true)
	if err != nil {
		return err
	}
	defer dir.Close()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	tmp := ".result-" + hex.EncodeToString(random[:])
	f, err := dir.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer dir.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	return dir.Rename(tmp, name)
}

func (e *Env) persistSessionResult(callID, content string) (string, error) {
	if e.sessionArtifactRoot == nil {
		return "", errors.New("session artifact root is unavailable")
	}
	name := sha256Hex([]byte(callID+"\x00"+content)) + ".txt"
	if err := e.sessionArtifactRoot.writeArchive(name, []byte(content)); err != nil {
		return "", err
	}
	return filepath.Join(e.SessionDir, "tool-results", name), nil
}
