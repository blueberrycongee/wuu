package wire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
)

// UserLocation is a content-free Personal destination. It never grants access:
// host and recipient identities are supplied by the authenticated transport.
// Version 1 identifies a named agent's main conversation, not an execution session.
type UserLocation struct {
	Version       int    `json:"version"`
	UserSpaceID   string `json:"user_space_id"`
	ProductDomain string `json:"product_domain"`
	AgentID       string `json:"agent_id"`
	TaskID        string `json:"task_id,omitempty"`
	MessageSeq    *int64 `json:"message_seq,omitempty"`
}

var pushLocationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$`)

// Validate rejects unsupported scopes and values that could carry free-form content.
func (l UserLocation) Validate() error {
	if l.Version != 1 || l.UserSpaceID != "local" || l.ProductDomain != "personal" || !pushLocationID.MatchString(l.AgentID) || (l.TaskID != "" && !pushLocationID.MatchString(l.TaskID)) || (l.MessageSeq != nil && *l.MessageSeq <= 0) {
		return errors.New("invalid personal push location")
	}
	return nil
}

// UnmarshalJSON refuses extra fields, including identities, content and session IDs.
func (l *UserLocation) UnmarshalJSON(data []byte) error {
	if len(data) > 1024 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("invalid personal push location")
	}
	type location UserLocation
	var parsed location
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["message_seq"]; ok && parsed.MessageSeq == nil {
		return errors.New("invalid personal push sequence")
	}
	if _, ok := fields["task_id"]; ok && parsed.TaskID == "" {
		return errors.New("invalid personal push task")
	}
	if err := UserLocation(parsed).Validate(); err != nil {
		return err
	}
	*l = UserLocation(parsed)
	return nil
}

// PushData produces the flat string payload consumed by native notification
// handlers. Callers must obtain host and recipient from authenticated state.
func (l UserLocation) PushData(host, recipient string) (map[string]string, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	for _, pub := range []string{host, recipient} {
		raw, err := base64.RawURLEncoding.DecodeString(pub)
		if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != pub {
			return nil, errors.New("invalid personal push identity")
		}
	}
	data := map[string]string{"version": "1", "user_space_id": l.UserSpaceID, "product_domain": l.ProductDomain, "agent_id": l.AgentID, "host": host, "recipient": recipient}
	if l.TaskID != "" {
		data["task_id"] = l.TaskID
	}
	if l.MessageSeq != nil {
		data["message_seq"] = strconv.FormatInt(*l.MessageSeq, 10)
	}
	return data, nil
}
