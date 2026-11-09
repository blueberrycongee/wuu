package host

import "testing"

func TestSpoolReplayAndAck(t *testing.T) {
	s := newSpool()
	s.append(1, []byte("a"))
	s.append(2, []byte("b"))
	s.append(3, []byte("c"))

	entries, ok := s.replayFrom(0)
	if !ok || len(entries) != 3 {
		t.Fatalf("replayFrom(0) = %v, %v", entries, ok)
	}
	entries, ok = s.replayFrom(2)
	if !ok || len(entries) != 1 || entries[0].seq != 3 {
		t.Fatalf("replayFrom(2) = %v, %v", entries, ok)
	}

	s.ackTo(2)
	if _, ok := s.replayFrom(0); ok {
		t.Fatalf("replay before acked base should fail")
	}
	entries, ok = s.replayFrom(2)
	if !ok || len(entries) != 1 {
		t.Fatalf("replayFrom(2) after ack = %v, %v", entries, ok)
	}
	// Acking everything leaves an empty but resumable spool.
	s.ackTo(3)
	entries, ok = s.replayFrom(3)
	if !ok || len(entries) != 0 {
		t.Fatalf("replayFrom(3) after full ack = %v, %v", entries, ok)
	}
}

func TestSpoolOverflowBreaks(t *testing.T) {
	s := newSpool()
	s.maxEntries = 4
	for i := uint64(1); i <= 5; i++ {
		s.append(i, []byte("x"))
	}
	if !s.broken {
		t.Fatalf("spool should be broken after overflow")
	}
	if _, ok := s.replayFrom(0); ok {
		t.Fatalf("broken spool must refuse replay")
	}
	if got := len(s.entries); got != 0 {
		t.Fatalf("broken spool retained %d entries", got)
	}
}

func TestSpoolPartialAckPreservesReplayAcrossStreaming(t *testing.T) {
	s := newSpool()
	s.maxEntries = 3
	s.maxBytes = 6
	s.append(1, []byte("aa"))
	s.append(2, []byte("bb"))
	s.append(3, []byte("cc"))
	snapshot, ok := s.replayFrom(0)
	if !ok {
		t.Fatal("initial stream cannot be replayed")
	}

	s.ackTo(2)
	s.ackTo(1) // A repeated or older acknowledgement must not reopen a gap.
	s.append(4, []byte("dd"))
	s.append(5, []byte("ee"))
	if s.broken || s.bytes != 6 {
		t.Fatalf("acknowledged frames still consume the spool limit: %+v", s)
	}
	if _, ok := s.replayFrom(1); ok {
		t.Fatal("replay crossed the acknowledged boundary")
	}
	entries, ok := s.replayFrom(2)
	if !ok || len(entries) != 3 {
		t.Fatalf("pending stream cannot resume: entries=%v ok=%v", entries, ok)
	}
	for i, want := range []string{"cc", "dd", "ee"} {
		if entries[i].seq != uint64(i+3) || string(entries[i].line) != want {
			t.Fatalf("replay reordered or changed frame %d: %+v", i, entries[i])
		}
	}
	for i, want := range []string{"aa", "bb", "cc"} {
		if snapshot[i].seq != uint64(i+1) || string(snapshot[i].line) != want {
			t.Fatalf("acknowledging the stream changed an earlier replay: %+v", snapshot)
		}
	}

	s.ackTo(5)
	s.append(6, []byte("ff"))
	entries, ok = s.replayFrom(5)
	if !ok || len(entries) != 1 || entries[0].seq != 6 || string(entries[0].line) != "ff" {
		t.Fatalf("stream could not continue after a full acknowledgement: entries=%v ok=%v", entries, ok)
	}
}
