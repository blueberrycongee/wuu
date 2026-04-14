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
	// FileChanged fires after a tool successfully writes or edits a
	// file. Aligned with Claude Code's FileChanged hook event.
	FileChanged Event = "FileChanged"
)

var validEvents = map[Event]bool{
	PreToolUse:         true,
	PostToolUse:        true,
	PostToolUseFailure: true,
	UserPromptSubmit:   true,
	SessionStart:       true,
	SessionEnd:         true,
	Stop:               true,
	FileChanged:        true,
}

// IsValid returns true if ev is a recognized event.
func IsValid(ev Event) bool {
	return validEvents[ev]
}
