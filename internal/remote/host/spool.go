package host

// spool is the host-side replay buffer for one app-server connection: every
// outbound line is retained (with its seq) until the phone acknowledges it,
// so a phone that drops and re-handshakes can resume exactly where it left
// off. Mobile links churn constantly; this is what makes "start a turn, lock
// the phone, come back later" work without replaying the whole thread.
//
// The buffer is bounded. When an absent phone lets it overflow, the spool
// marks itself broken and empties; the next attach then gets a fresh
// app-server connection and refetches state over RPC instead of a replay.
// (Same recovery as codex's bounded outbound buffer, applied at the
// end-to-end layer so the relay never needs to hold plaintext.)
type spool struct {
	entries []spoolEntry
	bytes   int
	base    uint64 // seq of the oldest retained entry; nextSeq when empty
	broken  bool

	maxEntries int
	maxBytes   int
}

type spoolEntry struct {
	seq  uint64
	line []byte
}

const (
	defaultSpoolMaxEntries = 8192
	defaultSpoolMaxBytes   = 32 << 20
)

func newSpool() *spool {
	return &spool{base: 1, maxEntries: defaultSpoolMaxEntries, maxBytes: defaultSpoolMaxBytes}
}

// append retains one outbound line. seq values must be handed in strictly
// increasing order.
func (s *spool) append(seq uint64, line []byte) {
	if s.broken {
		return
	}
	s.entries = append(s.entries, spoolEntry{seq: seq, line: line})
	s.bytes += len(line)
	if len(s.entries) > s.maxEntries || s.bytes > s.maxBytes {
		s.broken = true
		s.entries = nil
		s.bytes = 0
	}
}

// ackTo drops every entry the phone has confirmed.
func (s *spool) ackTo(seq uint64) {
	i := 0
	for ; i < len(s.entries); i++ {
		if s.entries[i].seq > seq {
			break
		}
		s.bytes -= len(s.entries[i].line)
	}
	if i > 0 {
		// Release acknowledged payloads without copying the pending stream.
		clear(s.entries[:i])
		s.entries = s.entries[i:]
		if len(s.entries) == 0 {
			s.entries = nil
		}
	}
	if seq+1 > s.base {
		s.base = seq + 1
	}
}

// replayFrom returns every retained entry after the given seq, or ok=false
// when the spool cannot bridge the gap (broken, or the phone asks for lines
// older than what is retained).
func (s *spool) replayFrom(after uint64) ([]spoolEntry, bool) {
	if s.broken {
		return nil, false
	}
	if after+1 < s.base {
		return nil, false
	}
	out := make([]spoolEntry, 0, len(s.entries))
	for _, e := range s.entries {
		if e.seq > after {
			out = append(out, e)
		}
	}
	return out, true
}
