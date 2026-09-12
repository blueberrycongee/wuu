package host

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

// deviceSession is the host's view of one paired phone: the current
// end-to-end channel (nil while the phone is away) plus the persistent
// app-server connection that keeps agents running across phone drops.
//
// Locking rules: every Seal and relay send for this device happens under mu
// so channel counters and frame order stay aligned. Pipe writes into the
// app-server never happen under mu (the app-server's own output pumps back
// into onAppLine, which needs mu; holding it across a pipe write could
// deadlock).
type deviceSession struct {
	h      *Host
	devPub string

	mu            sync.Mutex
	channel       *secure.Channel
	attached      bool
	compressLines bool
	app           *appConn
	lastPush      time.Time
	// lastThreadPush throttles turn-bound push hints per thread so one busy
	// thread cannot starve nudges for the others. Guarded by mu.
	lastThreadPush map[string]time.Time
}

// pushThreadSlotCap bounds lastThreadPush. Once reached, entries older than
// the push interval are pruned before a new slot is recorded.
const pushThreadSlotCap = 256

// tryConsumePushSlot reports whether a push hint may fire now and, if so,
// consumes its throttle slot. Thread-bound hints throttle per
// (device, thread); hints without a thread (needs_input) fall back to the
// per-device window. Caller holds s.mu.
func (s *deviceSession) tryConsumePushSlot(hint, threadID string) bool {
	if hint == "" {
		return false
	}
	interval := s.h.pushMinInterval
	now := time.Now()
	if threadID == "" {
		if now.Sub(s.lastPush) < interval {
			return false
		}
		s.lastPush = now
		return true
	}
	if last, ok := s.lastThreadPush[threadID]; ok && now.Sub(last) < interval {
		return false
	}
	if s.lastThreadPush == nil {
		s.lastThreadPush = make(map[string]time.Time)
	}
	if len(s.lastThreadPush) >= pushThreadSlotCap {
		for id, at := range s.lastThreadPush {
			if now.Sub(at) >= interval {
				delete(s.lastThreadPush, id)
			}
		}
	}
	s.lastThreadPush[threadID] = now
	return true
}

// appConn is one in-process app-server instance (over pipes) bound to a
// phone. It survives phone disconnects: turns keep streaming into the spool
// until the phone comes back or the spool overflows.
type appConn struct {
	contID string // continuity id the phone quotes to resume
	cancel context.CancelFunc
	pipes  []io.Closer

	writeMu sync.Mutex
	inW     io.Writer

	// guarded by deviceSession.mu
	profile  string
	seq      uint64
	spool    *spool
	stateVer uint64
	running  map[string]wire.RunningTurn
	closed   bool
}

func (a *appConn) close() {
	if a.closed {
		return
	}
	a.closed = true
	a.cancel()
	for _, p := range a.pipes {
		_ = p.Close()
	}
}

// newAppConnLocked starts a fresh app-server connection for this device.
// Called with s.mu held; goroutine startup does not block.
func (s *deviceSession) newAppConnLocked(profile string) *appConn {
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()
	ctx, cancel := context.WithCancel(s.h.baseCtx)
	app := &appConn{
		contID:  randomID(),
		cancel:  cancel,
		pipes:   []io.Closer{serverInR, serverInW, serverOutR, serverOutW},
		inW:     serverInW,
		profile: normalizeClientProfile(profile),
		spool:   newSpool(),
		running: map[string]wire.RunningTurn{},
	}
	go func() {
		if s.h.appServer != nil {
			if err := s.h.appServer(ctx, serverInR, serverOutW); err != nil && ctx.Err() == nil {
				s.h.logf("remote app-server connection: %v", err)
			}
		} else {
			_ = appserver.RunStdioForDevice(ctx, s.h.rt, serverInR, serverOutW, devicePushRegistrar{store: s.h.store, devPub: s.devPub})
		}
		_ = serverOutW.Close()
	}()
	go s.pumpAppOutput(app, serverOutR)
	return app
}

// pumpAppOutput streams app-server stdout lines into the session.
func (s *deviceSession) pumpAppOutput(app *appConn, out io.Reader) {
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), maxAppLineBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) == 0 {
			continue
		}
		s.onAppLine(app, line)
	}
	s.mu.Lock()
	if s.app == app && !app.closed {
		app.close()
		s.sendSealedLocked(wire.E2EMsg{T: wire.E2EBye, Reason: "app-server connection closed"})
		s.attached = false
		s.channel = nil
	}
	s.mu.Unlock()
}

// onAppLine records one outbound app-server line and, when the phone is
// attached, delivers it (plus any state change) in seq order.
func (s *deviceSession) onAppLine(app *appConn, line []byte) {
	s.mu.Lock()
	if s.app != app || app.closed {
		s.mu.Unlock()
		return
	}
	stateChanged := app.observeLine(line)
	outLine := line
	if app.profile == wire.ClientProfileMobileChat || app.profile == wire.ClientProfileMobileActivity {
		filtered, keep := (mobileChatFilter{tools: app.profile == wire.ClientProfileMobileActivity}).line(line)
		if !keep {
			attached := s.attached && s.channel != nil
			if attached && stateChanged {
				s.sendStateLocked()
			}
			s.mu.Unlock()
			return
		}
		outLine = filtered
	}
	app.seq++
	seq := app.seq
	app.spool.append(seq, outLine)
	attached := s.attached && s.channel != nil
	if attached {
		s.sendSealedLocked(wire.E2EMsg{T: wire.E2ERPC, Seq: seq, Line: json.RawMessage(outLine)})
		if stateChanged {
			s.sendStateLocked()
		}
		s.mu.Unlock()
		return
	}
	hint, pushThread := classifyPushHint(outLine)
	shouldPush := s.tryConsumePushSlot(hint, pushThread)
	s.mu.Unlock()
	if shouldPush {
		s.h.sendPush(s.devPub, hint, pushThread)
	}
}

// observeLine tracks live turns from the notification stream. Guarded by
// deviceSession.mu via the caller.
func (a *appConn) observeLine(line []byte) bool {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ThreadID string `json:"thread_id"`
			TurnID   string `json:"turn_id"`
			Turn     struct {
				ID string `json:"id"`
			} `json:"turn"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &probe); err != nil || probe.Method == "" {
		return false
	}
	switch probe.Method {
	case appserver.NotificationTurnStarted:
		turnID := probe.Params.Turn.ID
		if turnID == "" {
			turnID = probe.Params.TurnID
		}
		a.running[probe.Params.ThreadID] = wire.RunningTurn{
			ThreadID:  probe.Params.ThreadID,
			TurnID:    turnID,
			StartedAt: time.Now().UTC().Format(time.RFC3339),
		}
		return true
	case appserver.NotificationTurnCompleted, appserver.NotificationTurnError:
		if _, ok := a.running[probe.Params.ThreadID]; ok {
			delete(a.running, probe.Params.ThreadID)
			return true
		}
	}
	return false
}

// classifyPushHint maps an outbound line that arrives while the phone is
// away to a content-free push hint. Server requests (the app-server asking
// its client something) mean the agent is blocked on input; turn completion
// and errors mean attention is wanted but nothing is blocked. Turn-bound
// hints also carry the thread id so a pusher can deep-link to the thread;
// needs_input is process-wide and returns no thread.
func classifyPushHint(line []byte) (hint, threadID string) {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ThreadID string `json:"thread_id"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return "", ""
	}
	if len(probe.ID) > 0 && probe.Method != "" {
		return wire.PushNeedsInput, ""
	}
	switch probe.Method {
	case appserver.NotificationTurnCompleted, appserver.NotificationTurnError:
		return wire.PushAgentDone, probe.Params.ThreadID
	}
	return "", ""
}

// setChannel installs a freshly handshaken channel. The phone still has to
// attach before rpc traffic flows.
func (s *deviceSession) setChannel(ch *secure.Channel) {
	s.mu.Lock()
	s.channel = ch
	s.attached = false
	s.mu.Unlock()
}

// detach drops the live channel (transport loss, presence offline). The
// app-server connection and spool stay put for the next attach.
func (s *deviceSession) detach() {
	s.mu.Lock()
	s.channel = nil
	s.attached = false
	s.mu.Unlock()
}

// handleSealed decrypts and dispatches one sealed frame from the phone.
func (s *deviceSession) handleSealed(body []byte) {
	s.mu.Lock()
	ch := s.channel
	if ch == nil {
		s.mu.Unlock()
		return
	}
	plain, err := ch.Open(body)
	if err != nil {
		s.mu.Unlock()
		s.h.logf("remote host: drop frame from %s: %v", secure.Fingerprint(mustDecodeKey(s.devPub)), err)
		return
	}
	msg, err := wire.DecodeE2E(plain)
	if err != nil {
		s.mu.Unlock()
		return
	}

	switch msg.T {
	case wire.E2EAttach:
		s.handleAttachLocked(msg)
		s.mu.Unlock()
	case wire.E2EAck:
		if s.app != nil {
			s.app.spool.ackTo(msg.Recv)
		}
		s.mu.Unlock()
	case wire.E2EPing:
		s.sendSealedLocked(wire.E2EMsg{T: wire.E2EPong})
		s.mu.Unlock()
	case wire.E2EBye:
		s.channel = nil
		s.attached = false
		s.mu.Unlock()
	case wire.E2ERPC:
		app := s.app
		attached := s.attached
		s.mu.Unlock()
		if app == nil || !attached || len(msg.Line) == 0 {
			return
		}
		s.forwardToApp(app, msg.Line)
	default:
		s.mu.Unlock()
	}
}

// handleAttachLocked resumes or rebuilds the app-server connection for a
// phone that just handshook. Called with s.mu held.
func (s *deviceSession) handleAttachLocked(msg wire.E2EMsg) {
	s.compressLines = msg.AcceptLineCompression == "gzip"
	var replay []spoolEntry
	resumed := false
	profile := normalizeClientProfile(msg.ClientProfile)
	if s.app != nil && !s.app.closed && msg.Prev == s.app.contID && s.app.profile == profile {
		if entries, ok := s.app.spool.replayFrom(msg.Recv); ok {
			replay = entries
			resumed = true
			s.app.spool.ackTo(msg.Recv)
		}
	}
	if !resumed {
		if s.app != nil {
			s.app.close()
		}
		s.app = s.newAppConnLocked(profile)
	}
	s.attached = true
	s.sendSealedLocked(wire.E2EMsg{
		T:          wire.E2EAttached,
		Session:    s.app.contID,
		Resumed:    resumed,
		ReplayFrom: msg.Recv + 1,
	})
	s.sendStateLocked()
	for _, e := range replay {
		s.sendSealedLocked(wire.E2EMsg{T: wire.E2ERPC, Seq: e.seq, Line: json.RawMessage(e.line)})
	}
}

// sendStateLocked pushes the host state snapshot. Called with s.mu held.
func (s *deviceSession) sendStateLocked() {
	if s.app == nil {
		return
	}
	s.app.stateVer++
	running := make([]wire.RunningTurn, 0, len(s.app.running))
	for _, r := range s.app.running {
		running = append(running, r)
	}
	info := s.h.hostInfo()
	s.sendSealedLocked(wire.E2EMsg{T: wire.E2EState, Ver: s.app.stateVer, Host: &info, Running: running})
}

// sendSealedLocked seals and ships one end-to-end message. Called with s.mu
// held so counter allocation and network order stay aligned.
func (s *deviceSession) sendSealedLocked(msg wire.E2EMsg) {
	if s.channel == nil {
		return
	}
	plain, err := encodeSessionMessage(msg, s.compressLines)
	if err != nil {
		return
	}
	sealed := s.channel.Seal(plain)
	if err := s.h.sendFrame(s.devPub, wire.WrapPayload(wire.KindSealed, sealed)); err != nil {
		// The relay write failed; the read loop will notice the dead
		// connection and detach every session. Dropping the channel here
		// keeps the counter stream consistent for the next handshake.
		s.channel = nil
		s.attached = false
	}
}

// forwardToApp writes one phone rpc line into the app-server. Never called
// with s.mu held.
func (s *deviceSession) forwardToApp(app *appConn, line json.RawMessage) {
	data := append([]byte(nil), line...)
	if len(data) > maxAppLineBytes {
		s.h.logf("remote host: drop oversized rpc line (%d bytes)", len(data))
		return
	}
	data = append(data, '\n')
	app.writeMu.Lock()
	defer app.writeMu.Unlock()
	_, _ = app.inW.Write(data)
}

// shutdown tears the session down completely (host exiting).
func (s *deviceSession) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.app != nil {
		s.app.close()
		s.app = nil
	}
	s.channel = nil
	s.attached = false
}

func randomID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "conn-fallback"
	}
	return hex.EncodeToString(b[:])
}

func mustDecodeKey(s string) []byte {
	raw, err := secure.DecodeKey(s)
	if err != nil {
		return []byte(strings.TrimSpace(s))
	}
	return raw
}
