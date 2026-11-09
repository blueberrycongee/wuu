package agentthread

import (
	"encoding/json"
	"strings"
)

type InterAgentCommunication struct {
	Author          AgentPath   `json:"author"`
	Recipient       AgentPath   `json:"recipient"`
	OtherRecipients []AgentPath `json:"other_recipients,omitempty"`
	Content         string      `json:"content"`
	TriggerTurn     bool        `json:"trigger_turn"`
	// ChangedFileOverlap carries sibling-agent file-overlap warnings on
	// completion notifications so the envelope stays a single JSON object
	// rather than text-after-JSON. Consumers read it via the parsed struct
	// or the IsAgentNotification name gate; the textual <changed_file_overlap>
	// suffix that used to be glued onto Content is gone.
	ChangedFileOverlap []string `json:"changed_file_overlap,omitempty"`
}

func NewInterAgentCommunication(author, recipient AgentPath, content string, triggerTurn bool) InterAgentCommunication {
	if author == "" {
		author = RootAgentPath()
	}
	if recipient == "" {
		recipient = RootAgentPath()
	}
	return InterAgentCommunication{
		Author:      author,
		Recipient:   recipient,
		Content:     strings.TrimSpace(content),
		TriggerTurn: triggerTurn,
	}
}

func (m InterAgentCommunication) String() string {
	data, err := json.Marshal(m)
	if err != nil {
		return `{"content":"inter-agent communication marshal failed"}`
	}
	return string(data)
}

func SubagentNotificationContent(agentPath string, status any) string {
	data, err := json.Marshal(map[string]any{
		"agent_path": strings.TrimSpace(agentPath),
		"status":     status,
	})
	if err != nil {
		data = []byte(`{"error":"marshal failed"}`)
	}
	return "<subagent_notification>\n" + string(data) + "\n</subagent_notification>"
}
