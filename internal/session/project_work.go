package session

import (
	"errors"
	"time"
)

var ErrProjectWorkChanged = errors.New("project work changed; read its current revision and retry")

// ProjectWork is the shared contract, separate from session and turn identity.
// Version guards concurrent state writes; Revision identifies user requirements.
// Operation receipts retain prior briefs and evidence without replaying effects.
type ProjectWork struct {
	ID               string            `json:"id"`
	ProjectID        string            `json:"project_id"`
	Version          int               `json:"version"`
	Revision         int               `json:"revision"`
	Title            string            `json:"title"`
	Brief            string            `json:"brief"`
	Acceptance       string            `json:"acceptance"`
	Authority        string            `json:"authority"`
	SourceRefs       string            `json:"source_refs,omitempty"`
	Phase            string            `json:"phase"`
	LeadID           string            `json:"lead_id"`
	ExecutorID       string            `json:"executor_id,omitempty"`
	Workspace        string            `json:"workspace"`
	LeadModel        RuntimeSelection  `json:"lead_model"`
	ExecutorModel    RuntimeSelection  `json:"executor_model"`
	LeadInput        *ProjectWorkInput `json:"lead_input,omitempty"`
	ExecutorInput    *ProjectWorkInput `json:"executor_input,omitempty"`
	Summary          string            `json:"summary,omitempty"`
	Evidence         string            `json:"evidence,omitempty"`
	CodeRef          string            `json:"code_ref,omitempty"`
	SubmissionTurnID string            `json:"submission_turn_id,omitempty"`
	Review           string            `json:"review,omitempty"`
	ReviewTurnID     string            `json:"review_turn_id,omitempty"`
	DeliveryBy       string            `json:"delivery_by,omitempty"`
	Delivery         string            `json:"delivery,omitempty"`
	Blocker          string            `json:"blocker,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ProjectWorkInput is a durable outbox entry. The inbox client ID is also the
// consumption receipt; retrying after a crash never starts the same brief twice.
type ProjectWorkInput struct {
	ClientID string `json:"client_id"`
	SenderID string `json:"sender_id"`
	Brief    string `json:"brief"`
	Revision int    `json:"revision"`
}
