package pluginhost

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/readcursor"
)

const readCursorLifetime = 30 * time.Minute
const maxPluginReadCursorBytes = 4096
const maxReadCursorPlainBytes = 8192
const maxReadCursorTokenBytes = 12000

type ReadCursorOwner struct {
	SessionID, ActorID, ActorPath, CWD, PluginID, ToolID string
	RemoteBackend, RemoteIdentity, RemoteRoot            string
}
type ReadCursorState struct {
	SessionID, ActorID, ActorPath, CWD, PluginID, ToolID string
	RemoteBackend, RemoteIdentity, RemoteRoot            string
	WorkerInstance, NativeSHA256                         string
	Path                                                 string
	Offset, Limit, TotalLines                            int
	PluginCursor                                         string
}

func (s ReadCursorState) ReadCursorOwner() ReadCursorOwner {
	return ReadCursorOwner{SessionID: s.SessionID, ActorID: s.ActorID, ActorPath: s.ActorPath, CWD: s.CWD, PluginID: s.PluginID, ToolID: s.ToolID, RemoteBackend: s.RemoteBackend, RemoteIdentity: s.RemoteIdentity, RemoteRoot: s.RemoteRoot}
}

type readCursorEnvelope struct {
	State   ReadCursorState
	Expires int64
}
type readCursorStore struct {
	mu          sync.Mutex
	closed      bool
	initialized bool
	key         [32]byte
	noncePrefix [4]byte
	nonceCount  uint64
	retired     map[string]struct{}
	clock       func() time.Time
}

func (s *readCursorStore) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}
func (s *readCursorStore) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (h *Host) IssueReadCursor(state ReadCursorState) (string, error) {
	if h == nil || strings.TrimSpace(state.SessionID) == "" || strings.TrimSpace(state.CWD) == "" || strings.TrimSpace(state.PluginID) == "" || strings.TrimSpace(state.ToolID) == "" || strings.TrimSpace(state.Path) == "" || state.Offset < 1 || state.Limit < 0 || state.TotalLines < 0 || state.PluginCursor == "" || len(state.PluginCursor) > maxPluginReadCursorBytes {
		return "", errors.New("invalid external read continuation")
	}
	remote := state.RemoteBackend != "" || state.RemoteIdentity != "" || state.RemoteRoot != "" || state.WorkerInstance != "" || state.NativeSHA256 != ""
	if remote {
		digest, err := hex.DecodeString(state.NativeSHA256)
		if state.RemoteBackend == "" || state.RemoteIdentity == "" || state.RemoteRoot == "" || state.WorkerInstance == "" || err != nil || len(digest) != 32 {
			return "", errors.New("invalid remote read continuation")
		}
	}
	store := &h.readCursors
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return "", errors.New("external read continuation unavailable")
	}
	if _, retired := store.retired[state.PluginID]; retired {
		return "", errors.New("external read continuation owner is retired")
	}
	if !store.initialized {
		if _, err := rand.Read(store.key[:]); err != nil {
			return "", err
		}
		if _, err := rand.Read(store.noncePrefix[:]); err != nil {
			clear(store.key[:])
			return "", err
		}
		store.initialized = true
	}
	plaintext, err := json.Marshal(readCursorEnvelope{State: state, Expires: store.now().Add(readCursorLifetime).UnixNano()})
	if err != nil {
		return "", err
	}
	if len(plaintext) > maxReadCursorPlainBytes {
		return "", errors.New("external read continuation is too large")
	}
	aead, err := store.aead()
	if err != nil {
		return "", err
	}
	if store.nonceCount == ^uint64(0) {
		return "", errors.New("external read continuation nonce exhausted")
	}
	store.nonceCount++
	nonce := make([]byte, aead.NonceSize())
	copy(nonce, store.noncePrefix[:])
	binary.BigEndian.PutUint64(nonce[len(store.noncePrefix):], store.nonceCount)
	sealed := aead.Seal(nonce, nonce, plaintext, []byte(readcursor.Prefix))
	token := readcursor.Prefix + base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > maxReadCursorTokenBytes {
		return "", errors.New("external read continuation is too large")
	}
	return token, nil
}
func (h *Host) ResolveReadCursor(token string, owner ReadCursorOwner) (ReadCursorState, error) {
	unavailable := func() (ReadCursorState, error) {
		return ReadCursorState{}, errors.New("external read continuation unavailable or expired")
	}
	if h == nil || len(token) > maxReadCursorTokenBytes || !strings.HasPrefix(token, readcursor.Prefix) {
		return unavailable()
	}
	store := &h.readCursors
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || !store.initialized {
		return unavailable()
	}
	if _, retired := store.retired[owner.PluginID]; retired {
		return unavailable()
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token, readcursor.Prefix))
	if err != nil {
		return unavailable()
	}
	aead, err := store.aead()
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return unavailable()
	}
	nonce := sealed[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, sealed[aead.NonceSize():], []byte(readcursor.Prefix))
	if err != nil || len(plaintext) > maxReadCursorPlainBytes {
		return unavailable()
	}
	var envelope readCursorEnvelope
	if json.Unmarshal(plaintext, &envelope) != nil || !store.now().Before(time.Unix(0, envelope.Expires)) || envelope.State.ReadCursorOwner() != owner {
		return unavailable()
	}
	return envelope.State, nil
}
func (h *Host) closeReadCursors() {
	h.readCursors.mu.Lock()
	defer h.readCursors.mu.Unlock()
	h.readCursors.closed = true
	clear(h.readCursors.key[:])
	clear(h.readCursors.noncePrefix[:])
	h.readCursors.nonceCount = 0
	h.readCursors.initialized = false
	h.readCursors.retired = nil
}
func (h *Host) retireReadCursors(pluginID string) {
	store := &h.readCursors
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.retired == nil {
		store.retired = make(map[string]struct{})
	}
	store.retired[pluginID] = struct{}{}
}
