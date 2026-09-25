package host

import (
	"testing"
	"time"
)

func TestClassifyPushHintTurnCompleted(t *testing.T) {
	line := []byte(`{"method":"turn/completed","params":{"thread_id":"thread-1","turn":{"id":"turn-1"}}}`)
	hint, threadID := classifyPushHint(line)
	if hint != "agent_done" {
		t.Errorf("hint: want agent_done, got %q", hint)
	}
	if threadID != "thread-1" {
		t.Errorf("threadID: want thread-1, got %q", threadID)
	}
}

func TestClassifyPushHintTurnError(t *testing.T) {
	line := []byte(`{"method":"turn/error","params":{"thread_id":"thread-2","turn_id":"turn-2","error":"boom"}}`)
	hint, threadID := classifyPushHint(line)
	if hint != "agent_done" {
		t.Errorf("hint: want agent_done, got %q", hint)
	}
	if threadID != "thread-2" {
		t.Errorf("threadID: want thread-2, got %q", threadID)
	}
}

func TestClassifyPushHintServerRequest(t *testing.T) {
	// Server requests (the app-server asking its client something) carry
	// both id and method; the agent is blocked on input. Thread is empty
	// because the request is process-wide, not thread-scoped.
	line := []byte(`{"id":"42","method":"tool/approval","params":{}}`)
	hint, threadID := classifyPushHint(line)
	if hint != "needs_input" {
		t.Errorf("hint: want needs_input, got %q", hint)
	}
	if threadID != "" {
		t.Errorf("threadID: want empty for needs_input, got %q", threadID)
	}
}

func TestClassifyPushHintIrrelevant(t *testing.T) {
	cases := []string{
		`{"method":"thread/started","params":{"thread_id":"t-1"}}`,
		`{"method":"item/started","params":{}}`,
		`not json`,
		``,
	}
	for _, raw := range cases {
		hint, threadID := classifyPushHint([]byte(raw))
		if hint != "" || threadID != "" {
			t.Errorf("classifyPushHint(%q): want empty, got hint=%q thread=%q", raw, hint, threadID)
		}
	}
}

// tryConsumePushSlot needs a session with s.h.pushMinInterval set. We
// construct a minimal Host with just the field the throttle reads so the
// test stays focused.
func newThrottleTestSession(interval time.Duration) *deviceSession {
	return &deviceSession{
		h: &Host{pushMinInterval: interval},
	}
}

func TestTryConsumePushSlotPerThread(t *testing.T) {
	s := newThrottleTestSession(30 * time.Second)
	// First push for t-1 consumes the per-thread slot.
	if !s.tryConsumePushSlot("agent_done", "t-1") {
		t.Fatalf("first push t-1: want true, got false")
	}
	// A different thread can fire independently (per-(device, thread) is
	// the rule, not a per-device coarse cap).
	if !s.tryConsumePushSlot("agent_done", "t-2") {
		t.Errorf("push t-2 after t-1: want true, got false")
	}
	if !s.tryConsumePushSlot("agent_done", "t-3") {
		t.Errorf("push t-3 after t-1+t-2: want true, got false")
	}
	// But a second push on t-1 within its window is blocked.
	if s.tryConsumePushSlot("agent_done", "t-1") {
		t.Errorf("push t-1 second time within per-thread window: want false, got true")
	}
	// And a second push on t-2 within its window is blocked too.
	if s.tryConsumePushSlot("agent_done", "t-2") {
		t.Errorf("push t-2 second time within per-thread window: want false, got true")
	}
}

func TestTryConsumePushSlotPerDeviceCoarseThrottle(t *testing.T) {
	// Use a 1ms interval so we can wait it out without slowing the test.
	s := newThrottleTestSession(1 * time.Millisecond)
	if !s.tryConsumePushSlot("agent_done", "t-1") {
		t.Fatalf("first push: want true, got false")
	}
	// Wait past the interval.
	time.Sleep(10 * time.Millisecond)
	// Per-device window has elapsed; the same thread can fire again.
	if !s.tryConsumePushSlot("agent_done", "t-1") {
		t.Errorf("push t-1 after window: want true, got false")
	}
}

func TestTryConsumePushSlotEmptyThreadSkipsPerThreadThrottle(t *testing.T) {
	s := newThrottleTestSession(30 * time.Second)
	// needs_input has no thread_id; only per-device throttle applies.
	if !s.tryConsumePushSlot("needs_input", "") {
		t.Fatalf("first needs_input: want true, got false")
	}
	if s.tryConsumePushSlot("needs_input", "") {
		t.Errorf("second needs_input within window: want false, got true")
	}
}

func TestTryConsumePushSlotPrunesOnGrowth(t *testing.T) {
	s := newThrottleTestSession(1 * time.Millisecond)
	// Seed 300 distinct thread slots so the cap kicks in. Each push needs
	// to be spaced at least 1ms apart, so the test is slow but bounded.
	for i := 0; i < 300; i++ {
		// Wait just over the interval between each push to keep the
		// per-device throttle from blocking.
		time.Sleep(2 * time.Millisecond)
		threadID := "t-" + itoa(i)
		s.tryConsumePushSlot("agent_done", threadID)
	}
	// The map should have been pruned at least once.
	if len(s.lastThreadPush) > 256 {
		t.Errorf("lastThreadPush: want pruned to <= 256, got %d", len(s.lastThreadPush))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
