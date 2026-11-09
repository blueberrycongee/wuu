package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

type faultRule struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	ID        string `json:"id,omitempty"`
}
type faultControl struct {
	Version int         `json:"version"`
	Faults  []faultRule `json:"faults"`
}

func invoke(ctx context.Context, home string, provider *persistence.Service, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method != "commit" && params.Method != "import" && params.Method != "compact" {
		return provider.InvokeService(ctx, params)
	}
	if _, err := syntheticHome(home); err != nil {
		return nil, err
	}
	gate, err := fixturePath(home, fixtureDir+"/gate")
	if err != nil {
		return nil, err
	}
	// Every fixture process holds this separate gate through preflight and commit.
	// The real provider keeps its own authority lock, CAS and receipt validation.
	lock, err := storelock.AcquireContext(ctx, gate)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	if params.Method != "commit" || provider.Status().State != pluginhost.StateActive {
		return provider.InvokeService(ctx, params)
	}
	controlPath, err := fixturePath(home, fixtureDir+"/faults.json")
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(controlPath)
	if errors.Is(err, os.ErrNotExist) {
		return provider.InvokeService(ctx, params)
	}
	if err != nil {
		return nil, err
	}
	var tx persistence.Transaction
	if err := json.Unmarshal(params.Params, &tx); err != nil {
		return provider.InvokeService(ctx, params)
	}
	snapshotParams := params
	snapshotParams.Method = "export"
	snapshotParams.Params = json.RawMessage(`{}`)
	snapshotRaw, err := provider.InvokeService(ctx, snapshotParams)
	if err != nil {
		return nil, err
	}
	var snapshot persistence.Snapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return nil, err
	}
	for _, receipt := range snapshot.Receipts {
		if receipt.ID == tx.ID {
			return provider.InvokeService(ctx, params)
		}
	}
	if snapshot.Revision != tx.ExpectedRevision {
		return provider.InvokeService(ctx, params)
	}
	var control faultControl
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&control); err != nil {
		return nil, fmt.Errorf("invalid fixture fault control: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("fixture fault control must contain one JSON object")
	}
	if control.Version != 1 {
		return nil, errors.New("fixture fault control version must be 1")
	}
	for _, rule := range control.Faults {
		switch rule.Kind {
		case "edit-binding", "native-boundary":
			if rule.SessionID == "" {
				return nil, errors.New("fault requires session_id")
			}
		case "ack-history":
			if rule.ClientID == "" {
				return nil, errors.New("fault requires client_id")
			}
		case "claim", "release":
			if rule.ID == "" {
				return nil, errors.New("fault requires id")
			}
		case "fork-create":
		default:
			return nil, fmt.Errorf("unknown fixture fault %q", rule.Kind)
		}
	}
	type key struct{ namespace, id string }
	documents := make(map[key]json.RawMessage, len(snapshot.Documents))
	for _, doc := range snapshot.Documents {
		documents[key{doc.Namespace, doc.Key}] = doc.Value
	}
	for _, write := range tx.Writes {
		if write.Delete {
			continue
		}
		prior := documents[key{write.Namespace, write.Key}]
		for _, rule := range control.Faults {
			if !rule.matches(write, prior) {
				continue
			}
			path, err := fixturePath(home, fixtureDir+"/rejections.jsonl")
			if err != nil {
				return nil, err
			}
			event, err := json.Marshal(struct {
				At        time.Time            `json:"at"`
				Rule      faultRule            `json:"rule"`
				ID        string               `json:"transaction_id"`
				Revision  persistence.Revision `json:"expected_revision,string"`
				Namespace string               `json:"namespace"`
				Key       string               `json:"key"`
			}{time.Now().UTC(), rule, tx.ID, tx.ExpectedRevision, write.Namespace, write.Key})
			if err != nil {
				return nil, err
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				return nil, err
			}
			_, writeErr := file.Write(append(event, '\n'))
			err = errors.Join(writeErr, file.Sync(), file.Close())
			if err != nil {
				return nil, err
			}
			return nil, &pluginhost.HostServiceError{Code: "invalid_request", Message: "synthetic commit rejected before apply: " + rule.Kind}
		}
	}
	return provider.InvokeService(ctx, params)
}

func (rule faultRule) matches(write persistence.Write, prior json.RawMessage) bool {
	type fields struct {
		ID           string `json:"id"`
		EngineRef    string `json:"engine_ref"`
		EngineTurnID string `json:"engine_turn_id"`
		ClientID     string `json:"client_id"`
		State        string `json:"State"`
	}
	var old, next fields
	if json.Unmarshal(write.Value, &next) != nil {
		return false
	}
	if prior != nil && json.Unmarshal(prior, &old) != nil {
		return false
	}
	switch rule.Kind {
	case "edit-binding":
		return write.Namespace == "wuu.sessions.v1" && write.Key == rule.SessionID && old.EngineRef != "" && old.EngineRef != next.EngineRef
	case "fork-create":
		return write.Namespace == "wuu.sessions.v1" && prior == nil
	case "native-boundary":
		return write.Namespace == "wuu.session_messages.v1" && strings.HasPrefix(write.Key, base64.RawURLEncoding.EncodeToString([]byte(rule.SessionID))+"/") && next.EngineTurnID != ""
	case "ack-history":
		return write.Namespace == "wuu.session_messages.v1" && prior == nil && next.ClientID == rule.ClientID
	case "claim":
		return write.Namespace == "wuu.held_user_work.v1" && old.ID == rule.ID && next.ID == rule.ID && old.State == "pending" && next.State == "dispatching"
	case "release":
		return write.Namespace == "wuu.held_user_work.v1" && old.ID == rule.ID && next.ID == rule.ID && old.State == "dispatching" && next.State == "pending"
	}
	return false
}
