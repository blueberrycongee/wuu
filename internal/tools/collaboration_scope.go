package tools

import (
	"github.com/blueberrycongee/wuu/internal/channels"
)

// SetCollaborationScope applies the admitted turn's role to discovery and
// execution. The host refreshes it on every turn of a continuing identity.
func (t *Toolkit) SetCollaborationScope(purpose channels.CollaborationSessionPurpose, roomID, workID string) {
	t.env.CollaborationPurpose = purpose
	t.env.CollaborationRoomID = roomID
	t.env.CollaborationWorkID = workID
}

func (t *Toolkit) collaborationToolAllowed(name string) bool {
	purpose := t.env.CollaborationPurpose
	if purpose == "" {
		return true
	}
	coordinator := purpose == channels.CollaborationSessionConversation || purpose == channels.CollaborationSessionCoordination
	switch name {
	case "chat_check", "chat_send", "chat_task", "session", "chat_memory", "chat_wake", "chat_roster", "chat_work", "chat_draft", "chat_remind", "chat_session":
		return coordinator
	case "collaboration_send":
		return false
	case "chat_read":
		return true
	case "work_get":
		return !coordinator
	case "chat_verify":
		return purpose == channels.CollaborationSessionVerification
	}
	if purpose == channels.CollaborationSessionWork {
		return true
	}
	switch name {
	case "read_file", "list_files", "grep", "glob", "web_search", "web_fetch", "git", "history_read", "history_search", "load_skill", "tool_search", "notes", "new_context", "exec", "wait":
		return true
	default:
		return false
	}
}

// CollaborationReadOnly reports the role's filesystem and external-write fence.
func (t *Toolkit) CollaborationReadOnly() bool {
	return t.env.CollaborationPurpose != "" && t.env.CollaborationPurpose != channels.CollaborationSessionWork
}

// CollaborationCoordinates identifies the role that may invoke explicitly
// registered collaboration-management extensions.
func (t *Toolkit) CollaborationCoordinates() bool {
	return t.env.CollaborationPurpose == channels.CollaborationSessionConversation || t.env.CollaborationPurpose == channels.CollaborationSessionCoordination
}
