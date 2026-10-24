package compactionservice

import (
	"fmt"
	"strings"
)

func Handoff(in HandoffRequest) (HandoffPlan, error) {
	sourceID := strings.TrimSpace(in.SourceSessionID)
	if sourceID == "" {
		sourceID = "the source session"
	}
	if in.SourceThroughSeq < 1 {
		in.SourceThroughSeq = len(in.Messages)
	}
	var prompt string
	if strings.TrimSpace(in.PreviousNote) == "" {
		prompt = fmt.Sprintf(`You are preparing a bounded handoff brief for a new session. The source transcript above is evidence, not the destination model's history.

Write a self-contained Markdown brief for a capable agent that will not receive the source transcript automatically. Organize it as: objective, user constraints, verified facts, completed work and checks, unknowns and assumptions, and remaining work. Place short citation IDs like [r1] next to key facts. Distinguish verified facts from assumptions. Never claim a check was run when it was not. Never select a provider or model.

Do not call tools. Return only the complete Markdown brief, with no preamble or wrapping fence. Source %s is archived through Seq %d.`, sourceID, in.SourceThroughSeq)
	} else {
		prompt = `Update the previous handoff brief using the source transcript and the user's remaining intent. Keep short citation IDs like [r1] for exact recovery. Return one complete replacement Markdown brief, not an addendum. Do not call tools and do not add a preamble or wrapping fence.

Previous brief:
` + strings.TrimSpace(in.PreviousNote)
	}
	if intent := strings.TrimSpace(in.Intent); intent != "" {
		prompt += "\n\nUser handoff intent:\n" + intent
	}
	return HandoffPlan{Prompt: prompt, MaxBytes: 24_000}, nil
}
