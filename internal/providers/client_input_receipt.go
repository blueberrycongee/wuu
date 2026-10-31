package providers

// ClientInputReceipt describes durable acceptance independently of execution.
// InHistory means local history admission; ReadAt records inclusion in an agent model context.
type ClientInputReceipt struct {
	RequestID      string `json:"request_id"`
	ReceiptID      string `json:"receipt_id"`
	HostRequestID  string `json:"host_request_id"`
	ClientID       string `json:"client_id"`
	QueueID        string `json:"queue_id"`
	SessionID      string `json:"session_id"`
	UserSpaceID    string `json:"user_space_id,omitempty"`
	ProductDomain  string `json:"product_domain,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	PluginID       string `json:"plugin_id"`
	Owner          string `json:"owner"`
	MessageSeq     int    `json:"message_seq,omitempty"`
	HostTurnID     string `json:"hostturn_id,omitempty"`
	ReceiptState   string `json:"receipt_state"`
	ExecutionState string `json:"execution_state"`
	AcceptedAt     string `json:"accepted_at"`
	ReadAt         string `json:"read_at,omitempty"`
	Revision       int64  `json:"revision"`
	ErrorStage     string `json:"error_stage,omitempty"`
	Error          string `json:"error,omitempty"`
}
