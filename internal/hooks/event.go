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
	PermissionRequest  Event = "PermissionRequest"
	PreCompact         Event = "PreCompact"
	PostCompact        Event = "PostCompact"
	SubagentStart      Event = "SubagentStart"
	SubagentStop       Event = "SubagentStop"
	// FileChanged fires after a tool successfully writes or edits a file.
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
	PermissionRequest:  true,
	PreCompact:         true,
	PostCompact:        true,
	SubagentStart:      true,
	SubagentStop:       true,
	FileChanged:        true,
}

// AllEvents returns the complete supported hook vocabulary in stable order.
func AllEvents() []Event {
	return []Event{
		PreToolUse,
		PermissionRequest,
		PostToolUse,
		PostToolUseFailure,
		PreCompact,
		PostCompact,
		UserPromptSubmit,
		SubagentStart,
		SubagentStop,
		SessionStart,
		SessionEnd,
		Stop,
		FileChanged,
	}
}

// IsValid returns true if ev is a recognized event.
func IsValid(ev Event) bool {
	return validEvents[ev]
}
