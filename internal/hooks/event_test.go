package hooks

import "testing"

func TestEventString(t *testing.T) {
	if string(PreToolUse) != "PreToolUse" {
		t.Fatal("event string mismatch")
	}
}

func TestIsValidEvent(t *testing.T) {
	if !IsValid(PreToolUse) {
		t.Fatal("expected PreToolUse to be valid")
	}
	if IsValid(Event("FakeEvent")) {
		t.Fatal("expected FakeEvent to be invalid")
	}
}
