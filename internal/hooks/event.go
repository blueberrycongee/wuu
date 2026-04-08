package hooks

// Event identifies a hook lifecycle event.
type Event string

const (
	PreToolUse         Event = "PreToolUse"
	PostToolUse        Event = "PostToolUse"
	PostToolUseFailure Event = "PostToolUseFailure"
	UserPromptSubmit   Event = "UserPromptSubmit"
	SessionStart       Event = "SessionStart"
	SessionEnd         Event = "SessionEnd"
	Stop               Event = "Stop"
)

var validEvents = map[Event]bool{
	PreToolUse:         true,
	PostToolUse:        true,
	PostToolUseFailure: true,
	UserPromptSubmit:   true,
	SessionStart:       true,
	SessionEnd:         true,
	Stop:               true,
}

// IsValid returns true if ev is a recognized event.
func IsValid(ev Event) bool {
	return validEvents[ev]
}
