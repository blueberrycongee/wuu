package toolresult

type Envelope struct {
	OK              bool           `json:"ok"`
	ToolCallID      string         `json:"tool_call_id,omitempty"`
	Revision        string         `json:"revision,omitempty"`
	Summary         string         `json:"summary,omitempty"`
	Data            map[string]any `json:"data,omitempty"`
	DataRef         string         `json:"data_ref,omitempty"`
	Truncated       bool           `json:"truncated,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
	Provenance      *Provenance    `json:"provenance,omitempty"`
	NextSuggestions []string       `json:"next_suggestions,omitempty"`
}

type Provenance struct {
	Agent       string   `json:"agent,omitempty"`
	StepID      string   `json:"step_id,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}
