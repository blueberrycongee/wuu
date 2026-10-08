// Package document is the shared revisioned authority for supported human and
// agent document mutations. External editors do not participate in its locks.
package document

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const MaxTextBytes = 2 * 1024 * 1024

type Ref struct {
	Authority    string `json:"authority"`
	Root         string `json:"root,omitempty"`
	Path         string `json:"path,omitempty"`
	ConnectionID string `json:"connectionId,omitempty"`
	DocumentID   string `json:"documentId,omitempty"`
}
type Snapshot struct {
	Ref            Ref     `json:"ref"`
	Revision       string  `json:"revision"`
	Text           *string `json:"text,omitempty"`
	MediaType      string  `json:"mediaType"`
	ReadOnlyReason string  `json:"readOnlyReason,omitempty"`
}
type MutationResult struct {
	Status   string    `json:"status"`
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	Current  *Snapshot `json:"current,omitempty"`
}
type WriteRequest struct {
	Ref          Ref    `json:"ref"`
	BaseRevision string `json:"baseRevision"`
	OperationID  string `json:"operationId"`
	Text         string `json:"text"`
}
type CreateRequest struct {
	Ref         Ref    `json:"ref"`
	OperationID string `json:"operationId"`
	Text        string `json:"text"`
}
type RenameRequest struct {
	Ref          Ref    `json:"ref"`
	BaseRevision string `json:"baseRevision"`
	OperationID  string `json:"operationId"`
	NewPath      string `json:"newPath"`
}

// Grant is derived from authenticated host context, never from caller actor fields.
type Grant struct {
	Root    string
	ActorID string
}
type Authorizer func(context.Context, Ref, bool) (Grant, error)
type Service struct {
	stateDir  string
	authorize Authorizer
}

func New(stateDir string, authorize Authorizer) *Service {
	return &Service{stateDir: stateDir, authorize: authorize}
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func relative(path string) (string, error) {
	path = strings.ReplaceAll(path, "\\", "/")
	if !filepath.IsLocal(path) || path == "." {
		return "", errors.New("document path must be a relative file")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return "", errors.New("document path must not traverse parents")
		}
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}
func noSymlinks(root *os.Root, path string, allowMissing bool) error {
	parts := strings.Split(filepath.ToSlash(path), "/")
	current := ""
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if os.IsNotExist(err) && allowMissing && i == len(parts)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("document paths must not contain symlinks")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return errors.New("document parent is not a directory")
		}
	}
	return nil
}
func (s *Service) open(ctx context.Context, ref Ref, write bool) (*os.Root, Ref, Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, ref, Grant{}, err
	}
	if ref.Authority != "local" {
		return nil, ref, Grant{}, errors.New("local document authority required")
	}
	if s.authorize == nil {
		return nil, ref, Grant{}, errors.New("document authorizer is required")
	}
	path, err := relative(ref.Path)
	if err != nil {
		return nil, ref, Grant{}, err
	}
	grant, err := s.authorize(ctx, ref, write)
	if err != nil {
		return nil, ref, grant, err
	}
	if grant.ActorID == "" || !filepath.IsAbs(grant.Root) {
		return nil, ref, grant, errors.New("authenticated actor and absolute authorized root required")
	}
	canonical, err := filepath.EvalSymlinks(grant.Root)
	if err != nil {
		return nil, ref, grant, err
	}
	requested, err := filepath.EvalSymlinks(ref.Root)
	if err != nil {
		return nil, ref, grant, err
	}
	if canonical != requested {
		return nil, ref, grant, errors.New("document root does not match authorized root")
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, ref, grant, err
	}
	ref.Root = canonical
	ref.Path = filepath.ToSlash(path)
	grant.Root = canonical
	return root, ref, grant, nil
}
func read(root *os.Root, ref Ref) (Snapshot, error) {
	result := Snapshot{Ref: ref}
	if err := noSymlinks(root, ref.Path, false); err != nil {
		return result, err
	}
	f, err := root.Open(ref.Path)
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("document must be a regular file")
	}
	// Unsupported large files are metadata-only. They cannot be mutated through
	// this authority, and reading them must not monopolize the shared write lock.
	if info.Size() > MaxTextBytes {
		result.Revision = "metadata:" + digest([]byte(fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())))
		result.MediaType = "application/octet-stream"
		result.ReadOnlyReason = "too_large"
		return result, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxTextBytes+1))
	if err != nil {
		return result, err
	}
	after, err := f.Stat()
	if err != nil {
		return result, err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || len(data) > MaxTextBytes {
		return result, errors.New("document changed during read")
	}
	result.Revision = digest(data)
	result.MediaType = http.DetectContentType(data)
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		result.ReadOnlyReason = "binary"
		return result, nil
	}
	text := string(data)
	result.Text = &text
	return result, nil
}
func (s *Service) Read(ctx context.Context, ref Ref) (Snapshot, error) {
	root, ref, _, err := s.open(ctx, ref, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	return read(root, ref)
}

type mutation struct {
	Kind         string `json:"kind"`
	Ref          Ref    `json:"ref"`
	BaseRevision string `json:"baseRevision,omitempty"`
	OperationID  string `json:"operationId"`
	Text         string `json:"text,omitempty"`
	NewPath      string `json:"newPath,omitempty"`
}
type journal struct {
	PayloadHash string          `json:"payloadHash"`
	Mutation    mutation        `json:"mutation"`
	Result      *MutationResult `json:"result,omitempty"`
}

func (s *Service) Write(ctx context.Context, r WriteRequest) (MutationResult, error) {
	return s.mutate(ctx, mutation{Kind: "write", Ref: r.Ref, BaseRevision: r.BaseRevision, OperationID: r.OperationID, Text: r.Text})
}
func (s *Service) Create(ctx context.Context, r CreateRequest) (MutationResult, error) {
	return s.mutate(ctx, mutation{Kind: "create", Ref: r.Ref, OperationID: r.OperationID, Text: r.Text})
}
func (s *Service) Rename(ctx context.Context, r RenameRequest) (MutationResult, error) {
	return s.mutate(ctx, mutation{Kind: "rename", Ref: r.Ref, BaseRevision: r.BaseRevision, OperationID: r.OperationID, NewPath: r.NewPath})
}
func saved(snapshot Snapshot) MutationResult {
	return MutationResult{Status: "saved", Snapshot: &snapshot}
}
func conflict(snapshot Snapshot) MutationResult {
	return MutationResult{Status: "conflict", Current: &snapshot}
}
func saveJournal(path string, j journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return securefs.WriteFileAtomic(path, data)
}
func (s *Service) mutate(ctx context.Context, m mutation) (MutationResult, error) {
	var zero MutationResult
	if strings.TrimSpace(m.OperationID) == "" || len(m.OperationID) > 256 {
		return zero, errors.New("operationId is required and limited to 256 bytes")
	}
	if m.Kind != "create" && m.BaseRevision == "" {
		return zero, errors.New("baseRevision is required")
	}
	if len(m.Text) > MaxTextBytes || !utf8.ValidString(m.Text) || strings.IndexByte(m.Text, 0) >= 0 {
		return zero, errors.New("document text must be bounded UTF-8 without NUL bytes")
	}
	root, ref, grant, err := s.open(ctx, m.Ref, true)
	if err != nil {
		return zero, err
	}
	defer root.Close()
	m.Ref = ref
	if m.Kind == "rename" {
		p, e := relative(m.NewPath)
		if e != nil {
			return zero, e
		}
		m.NewPath = filepath.ToSlash(p)
		destination := ref
		destination.Path = m.NewPath
		destinationRoot, _, destinationGrant, err := s.open(ctx, destination, true)
		if err != nil {
			return zero, err
		}
		destinationRoot.Close()
		if destinationGrant.ActorID != grant.ActorID {
			return zero, errors.New("rename actor changed")
		}
		if m.NewPath == ref.Path {
			return zero, errors.New("new path must differ")
		}
	}
	// A shared authority lock also covers nested registered roots that address
	// the same file; root-scoped locks alone would allow a CAS bypass.
	dir := filepath.Join(s.stateDir, "documents")
	lock, err := storelock.Acquire(dir)
	if err != nil {
		return zero, err
	}
	defer lock.Release()
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	payload, _ := json.Marshal(m)
	j := journal{PayloadHash: digest(payload), Mutation: m}
	receipt := filepath.Join(dir, digest([]byte(grant.ActorID+"\x00"+m.OperationID))+".json")
	prior, err := os.ReadFile(receipt)
	recovery := err == nil
	if err != nil && !os.IsNotExist(err) {
		return zero, err
	}
	if recovery {
		var old journal
		if err = json.Unmarshal(prior, &old); err != nil {
			return zero, err
		}
		if old.PayloadHash != j.PayloadHash {
			return zero, errors.New("operationId was already used with a different payload")
		}
		j = old
		if j.Result != nil {
			return *j.Result, nil
		}
	}
	finish := func(result MutationResult) (MutationResult, error) {
		j.Result = &result
		if err := saveJournal(receipt, j); err != nil {
			return zero, err
		}
		return result, nil
	}
	if recovery {
		if result, ok, err := reconcile(root, m); err != nil {
			return zero, err
		} else if ok {
			return finish(result)
		}
	}
	if err = noSymlinks(root, ref.Path, m.Kind == "create"); err != nil {
		return zero, err
	}
	current, readErr := read(root, ref)
	if m.Kind == "create" {
		if readErr == nil {
			return finish(conflict(current))
		}
		if !os.IsNotExist(readErr) {
			return zero, readErr
		}
	} else {
		if readErr != nil {
			return zero, readErr
		}
		if current.Revision != m.BaseRevision {
			return finish(conflict(current))
		}
		if current.ReadOnlyReason != "" {
			return zero, errors.New("document is read-only: " + current.ReadOnlyReason)
		}
	}
	if m.Kind == "rename" {
		if err = noSymlinks(root, m.NewPath, true); err != nil {
			return zero, err
		}
		if _, err = root.Lstat(m.NewPath); err == nil {
			return zero, errors.New("rename destination already exists")
		}
		if !os.IsNotExist(err) {
			return zero, err
		}
	}
	// Persist intent before touching user bytes. A restarted request reconciles a
	// completed write by its target hash instead of repeating create or rename.
	if err = saveJournal(receipt, j); err != nil {
		return zero, err
	}
	if m.Kind == "rename" {
		// Link is exclusive; unlike rename it cannot overwrite a concurrent target.
		if err = root.Link(ref.Path, m.NewPath); err != nil {
			return zero, err
		}
		linked, e := root.Stat(m.NewPath)
		if e != nil {
			return zero, e
		}
		result, e := finishLinkedRename(root, ref, m.NewPath, m.BaseRevision, linked)
		if e != nil {
			return zero, e
		}
		return finish(result)
	} else {
		var name [16]byte
		if _, err = rand.Read(name[:]); err != nil {
			return zero, err
		}
		temp := filepath.Join(filepath.Dir(ref.Path), ".wuu-document-"+hex.EncodeToString(name[:]))
		f, e := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return zero, e
		}
		defer root.Remove(temp)
		if m.Kind == "write" {
			info, e := root.Stat(ref.Path)
			if e != nil {
				f.Close()
				return zero, e
			}
			// Preserve existing access and executable bits; create remains private.
			if e = f.Chmod(info.Mode().Perm()); e != nil {
				f.Close()
				return zero, e
			}
		}
		_, err = f.Write([]byte(m.Text))
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return zero, err
		}
		if closeErr != nil {
			return zero, closeErr
		}
		if m.Kind == "create" {
			err = root.Link(temp, ref.Path)
		} else {
			again, e := read(root, ref)
			if e != nil {
				return zero, e
			}
			if again.Revision != m.BaseRevision {
				return finish(conflict(again))
			}
			err = root.Rename(temp, ref.Path)
		}
		if err != nil {
			return zero, err
		}
	}
	result, err := read(root, ref)
	if err != nil {
		return zero, err
	}
	return finish(saved(result))
}

// reconcile handles the only ambiguous interval: user bytes were changed but
// the durable result receipt was not written. Unrelated bytes are never removed.
func reconcile(root *os.Root, m mutation) (MutationResult, bool, error) {
	target := m.Ref
	wanted := digest([]byte(m.Text))
	if m.Kind == "rename" {
		target.Path = m.NewPath
		wanted = m.BaseRevision
	}
	snapshot, err := read(root, target)
	if os.IsNotExist(err) {
		return MutationResult{}, false, nil
	}
	if err != nil {
		return MutationResult{}, false, err
	}
	if snapshot.Revision != wanted {
		return MutationResult{}, false, nil
	}
	if m.Kind == "rename" {
		source, err := read(root, m.Ref)
		if err == nil {
			sourceInfo, sourceErr := root.Stat(m.Ref.Path)
			targetInfo, targetErr := root.Stat(target.Path)
			if sourceErr != nil || targetErr != nil || !os.SameFile(sourceInfo, targetInfo) {
				return MutationResult{}, false, errors.New("rename recovery source was replaced")
			}
			if source.Revision != m.BaseRevision {
				return MutationResult{}, false, fmt.Errorf("rename recovery source changed")
			}
			if err = root.Remove(m.Ref.Path); err != nil {
				return MutationResult{}, false, err
			}
		} else if !os.IsNotExist(err) {
			return MutationResult{}, false, err
		}
	}
	return saved(snapshot), true, nil
}

// finishLinkedRename validates the source after exclusive destination creation.
// Rollback only removes the inode this operation linked, never a replacement.
func finishLinkedRename(root *os.Root, ref Ref, target, expected string, linked os.FileInfo) (MutationResult, error) {
	cleanup := func() error {
		current, err := root.Stat(target)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !os.SameFile(current, linked) {
			return errors.New("rename destination replaced; partial operation requires review")
		}
		return root.Remove(target)
	}
	source, err := read(root, ref)
	sourceInfo, statErr := root.Stat(ref.Path)
	if err != nil || statErr != nil || source.Revision != expected || !os.SameFile(sourceInfo, linked) {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return MutationResult{}, errors.Join(errors.New("rename interrupted with destination remaining"), err, cleanupErr)
		}
		if err != nil {
			return MutationResult{}, err
		}
		if statErr != nil {
			return MutationResult{}, statErr
		}
		return conflict(source), nil
	}
	if err = root.Remove(ref.Path); err != nil {
		return MutationResult{}, errors.Join(err, cleanup())
	}
	source.Ref.Path = target
	return saved(source), nil
}
