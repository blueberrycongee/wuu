package hooks

import "encoding/json"

// Output is the parsed response from a hook process.
// All fields are optional; a hook that simply exits 0 produces a zero Output.
type Output struct {
	Continue     *bool           `json:"continue,omitempty"`
	Decision     string          `json:"decision,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`
	Context      string          `json:"additional_context,omitempty"`
}

// IsBlocked returns true when the hook wants to block the operation.
func (o *Output) IsBlocked() bool {
	if o == nil {
		return false
	}
	if o.Decision == "block" {
		return true
	}
	if o.Continue != nil && !*o.Continue {
		return true
	}
	return false
}

// ParseOutput interprets hook stdout. If stdout is valid JSON, it is decoded
// into Output. Exit code 2 always blocks, even when JSON says to continue.
// All other nonzero exit codes are execution failures handled by the caller.
func ParseOutput(stdout []byte, exitCode int) (*Output, error) {
	out := &Output{}
	if len(stdout) > 0 {
		var parsed Output
		if err := json.Unmarshal(stdout, &parsed); err == nil {
			out = &parsed
		}
	}
	if exitCode == 2 {
		out.Decision = "block"
	}
	return out, nil
}
