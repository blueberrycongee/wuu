package host

import (
	"bytes"
	"testing"
)

func BenchmarkSpoolStreaming(b *testing.B) {
	// Model scanner-owned streaming notifications retained until a phone ACK.
	// The small window covers a caught-up client; larger windows cover bursts
	// or a reconnect catching up while the stream continues.
	line := []byte(`{"method":"item/agentMessage/delta","params":{"thread_id":"thread","turn_id":"turn","item_id":"answer","delta":"streamed text"}}`)
	for _, tc := range []struct {
		name    string
		pending int
		batch   int
	}{
		{"caught-up", 1, 1},
		{"queued32", 32, 16},
		{"queued128", 128, 32},
		{"queued2048", 2048, 32},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s := newSpool()
			var sent, acked uint64
			for range tc.pending {
				sent++
				s.append(sent, bytes.Clone(line))
			}
			b.SetBytes(int64(len(line) * tc.batch))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				acked += uint64(tc.batch)
				s.ackTo(acked)
				for range tc.batch {
					sent++
					s.append(sent, bytes.Clone(line))
				}
			}
			b.StopTimer()
			entries, ok := s.replayFrom(acked)
			if s.broken || !ok || len(entries) != tc.pending || s.bytes != len(line)*tc.pending {
				b.Fatal("streaming benchmark lost pending notifications")
			}
		})
	}
}
