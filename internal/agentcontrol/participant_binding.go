package agentcontrol

import "strings"

// SetAgentParticipantID binds a running agent to the named-agent identity it
// represents. Restored runs use this identity for attribution and lifecycle
// bookkeeping; it does not grant conversation or roster-management tools.
func (c *AgentControl) SetAgentParticipantID(agentID, participantID string) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	participantID = strings.TrimSpace(participantID)
	c.participantBindingMu.Lock()
	defer c.participantBindingMu.Unlock()
	if participantID == "" {
		delete(c.participantBindings, agentID)
		return
	}
	if c.participantBindings == nil {
		c.participantBindings = make(map[string]string)
	}
	c.participantBindings[agentID] = participantID
}

// BoundParticipantID returns the named-agent identity bound to agentID.
func (c *AgentControl) BoundParticipantID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	c.participantBindingMu.Lock()
	defer c.participantBindingMu.Unlock()
	return c.participantBindings[agentID]
}
