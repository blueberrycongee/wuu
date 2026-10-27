package personalconnectors

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

type PollRequest struct {
	SourceID     string          `json:"source_id"`
	ConnectionID string          `json:"connection_id"`
	Operation    string          `json:"operation"`
	Arguments    json.RawMessage `json:"arguments"`
}
type PollResult struct {
	SourceID           string          `json:"source_id"`
	State              string          `json:"state"`
	Sequence           uint64          `json:"sequence"`
	ChangeID           string          `json:"change_id,omitempty"`
	ConnectionRevision string          `json:"connection_revision"`
	Snapshot           json.RawMessage `json:"snapshot"`
	Scope              string          `json:"scope"`
}
type pollState struct {
	Definition string `json:"definition"`
	Digest     string `json:"digest"`
	Revision   string `json:"revision"`
	Sequence   uint64 `json:"sequence"`
}

func pollCanonical(raw json.RawMessage) (string, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "", fault("invalid_arguments")
	}
	canonical, e := json.Marshal(value)
	if e != nil {
		return "", fault("invalid_arguments")
	}
	return string(canonical), nil
}

// Poll compares one explicitly selected bounded query page. It is not a mailbox
// delta cursor, webhook, scheduler, or guaranteed event-delivery queue. Persistent
// state is session-bound and contains hashes/revisions only; the first baseline never signals a change.
func (b *Broker) Poll(ctx context.Context, a Actor, q PollRequest) (PollResult, error) {
	if q.SourceID == "" || len(q.SourceID) > 128 || strings.ContainsAny(q.SourceID, "\x00\r\n") {
		return PollResult{}, fault("invalid_source_id")
	}
	allowed := []string{"gmail.search", "gmail.thread", "notion.search", "notion.page", "notion.blocks", "notion.database", "notion.query", "drive.search", "drive.file", "drive.export", "github.pr", "github.issue", "github.checks", "github.releases", "calendar.agenda", "slack.history", "slack.search", "slack.mentions", "feishu.history", "feishu.search", "feishu.mentions"}
	if !slices.Contains(allowed, q.Operation) {
		return PollResult{}, fault("poll_operation_not_supported")
	}
	arguments, e := pollCanonical(q.Arguments)
	if e != nil {
		return PollResult{}, e
	}
	definition := hash(q.ConnectionID + "\x00" + q.Operation + "\x00" + arguments)
	key := hash(a.SessionID + "\x00" + q.SourceID)
	var prior pollState
	var existed bool
	var revision string
	e = b.update(ctx, a, func(s *stored) error {
		g, ok := s.Grants[q.ConnectionID]
		if !ok {
			return fault("account_not_connected")
		}
		if g.Connection.Domain == "work" && g.Connection.WorkReadPolicy != "selected_resources" {
			return fault("confirmation_required")
		}
		p, err := build(g.Connection, q.Operation, q.Arguments)
		if err != nil {
			return err
		}
		if p.Write {
			return fault("poll_operation_not_supported")
		}
		revision = g.Connection.Revision
		prior, existed = s.Polls[key]
		if existed && prior.Definition != definition {
			return fault("poll_definition_changed")
		}
		if !existed && len(s.Polls) >= 1000 {
			return fault("poll_source_limit")
		}
		return nil
	})
	if e != nil {
		return PollResult{}, e
	}
	read, e := b.Call(ctx, a, Request{ConnectionID: q.ConnectionID, Operation: q.Operation, Arguments: q.Arguments})
	if e != nil {
		return PollResult{}, e
	}
	if read.State != "completed" {
		return PollResult{}, fault("poll_requires_read_authority")
	}
	canonical, e := pollCanonical(read.Data)
	if e != nil {
		return PollResult{}, fault("invalid_response")
	}
	digest := hash(canonical)
	result := PollResult{SourceID: q.SourceID, Snapshot: read.Data, Scope: "bounded_query_page", ConnectionRevision: revision}
	e = b.update(ctx, a, func(s *stored) error {
		result.State, result.ChangeID, result.Sequence = "", "", 0
		g, ok := s.Grants[q.ConnectionID]
		if !ok || g.Connection.Revision != revision || g.Connection.State != "connected" || !b.activated(a, g) {
			return fault("connection_changed")
		}
		current, exists := s.Polls[key]
		// A concurrent newer observation wins. Never roll back its digest with a
		// response from a request started against an older source generation.
		if exists != existed || exists && current != prior {
			if exists && current.Definition == definition && current.Revision == revision && current.Digest == digest {
				result.State = "unchanged"
				result.Sequence = current.Sequence
				return nil
			}
			return fault("poll_superseded")
		}
		next := pollState{Definition: definition, Digest: digest, Revision: revision, Sequence: prior.Sequence}
		result.State = "unchanged"
		if !exists || current.Revision != revision {
			result.State = "baseline"
		} else if current.Digest != digest {
			if current.Sequence == ^uint64(0) {
				return fault("poll_sequence_exhausted")
			}
			next.Sequence++
			result.State = "changed"
			result.ChangeID = hash(a.UserSpaceID + "\x00" + a.SessionID + "\x00" + q.SourceID + "\x00" + revision + "\x00" + strconv.FormatUint(next.Sequence, 10) + "\x00" + digest)
		}
		result.Sequence = next.Sequence
		s.Polls[key] = next
		return nil
	})
	if e != nil {
		return PollResult{}, e
	}
	return result, nil
}
