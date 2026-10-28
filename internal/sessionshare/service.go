package sessionshare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

const namespace = "session.sharing.v1"

var errUnavailable = errors.New("share unavailable or access denied")

type record struct {
	OperationID  string `json:"operation_id"`
	IntentDigest string `json:"intent_digest"`
	Grant
	Creator    principal.Identity   `json:"creator"`
	Recipients []principal.Identity `json:"recipients"`
	Selection  Selection            `json:"selection"`
	Frozen     Source               `json:"frozen"`
	Revoked    bool                 `json:"revoked"`
}
type Service struct {
	mu        sync.Mutex
	state     pluginhost.State
	documents persistence.Documents
	now       func() time.Time
	active    sync.WaitGroup
	pending   map[uint64]context.CancelFunc
	next      uint64
	stopped   chan struct{}
}

func NewService(documents persistence.Documents) *Service {
	return &Service{state: pluginhost.StatePrepared, documents: documents, now: time.Now, pending: map[uint64]context.CancelFunc{}}
}
func (s *Service) ID() string { return NativeModule }
func (s *Service) Status() pluginhost.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pluginhost.Status{ID: s.ID(), State: s.state}
}
func (s *Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, m := range []string{"preview", "create", "resolve", "read", "attachment", "revoke", "authorize"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: m, InputSchema: ServiceName + "." + m + ".input.v1", OutputSchema: ServiceName + "." + m + ".output.v1"})
	}
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: methods}}
}
func (s *Service) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (s *Service) Prepare(ctx context.Context) error { return ctx.Err() }
func (s *Service) Activate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.state != pluginhost.StatePrepared || s.documents == nil {
		return errors.New("sharing provider is not prepared")
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped == nil {
		s.state = pluginhost.StateStopped
		s.stopped = make(chan struct{})
		for _, cancel := range s.pending {
			cancel()
		}
		go func() { s.active.Wait(); close(s.stopped) }()
	}
	stopped := s.stopped
	s.mu.Unlock()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func decode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one sharing request")
	}
	return nil
}
func (s *Service) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.state != pluginhost.StateActive {
		s.mu.Unlock()
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "sharing provider is not active"}
	}
	ctx, cancel := context.WithCancel(ctx)
	s.next++
	id := s.next
	s.pending[id] = cancel
	s.active.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.pending, id); s.mu.Unlock(); s.active.Done() }()

	if p.Service != ServiceName || p.Caller == "" {
		return nil, errors.New("invalid sharing invocation")
	}
	var out any
	var err error
	switch p.Method {
	case "preview":
		var in PreviewRequest
		if err = decode(p.Params, &in); err == nil {
			out, err = s.preview(in)
		}
	case "create":
		var in CreateRequest
		if err = decode(p.Params, &in); err == nil {
			out, err = s.create(ctx, in)
		}
	case "resolve":
		var in ReadRequest
		if err = decode(p.Params, &in); err == nil {
			var r record
			r, _, err = s.readable(ctx, in)
			out = ReadPlan{Mode: r.Mode, Selection: r.Selection}
		}
	case "read":
		var in ReadRequest
		if err = decode(p.Params, &in); err == nil {
			out, err = s.read(ctx, in)
		}
	case "attachment":
		var in AttachmentRequest
		if err = decode(p.Params, &in); err == nil {
			out, err = s.attachment(ctx, in)
		}
	case "revoke":
		var in RevokeRequest
		if err = decode(p.Params, &in); err == nil {
			err = s.revoke(ctx, in)
			out = struct{}{}
		}
	case "authorize":
		var in access.Request
		if err = decode(p.Params, &in); err == nil {
			out, err = s.Authorize(ctx, in)
		}
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown sharing method"}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}
func (s *Service) preview(in PreviewRequest) (Preview, error) {
	document, items, err := Project(in.Source, in.Selection)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Document: document, ReviewDigest: digest(Source{SessionID: in.Source.SessionID, Items: items}), Warning: "Review selected text and attachments for secrets. No automatic secret detection is guaranteed. Downloaded copies cannot be revoked."}, nil
}
func (s *Service) create(ctx context.Context, in CreateRequest) (Grant, error) {
	if err := in.Actor.Validate(); err != nil {
		return Grant{}, err
	}
	if strings.TrimSpace(in.OperationID) == "" || len(in.OperationID) > 256 || strings.ContainsRune(in.OperationID, 0) {
		return Grant{}, errors.New("stable operation_id is required")
	}
	// A grant ID is an address, not a bearer credential. Binding it to the actor
	// and operation allows recovery even when the entire create response is lost.
	id := digest(struct {
		Actor       principal.Identity
		OperationID string
	}{in.Actor, in.OperationID})
	intent := in
	intent.Source = Source{SessionID: in.Source.SessionID}
	intentDigest := digest(intent)
	var grant Grant
	err := persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var existing record
		found, err := tx.Get(namespace, id, &existing)
		if err != nil {
			return err
		}
		if found {
			if existing.Creator != in.Actor || existing.OperationID != in.OperationID || existing.IntentDigest != intentDigest {
				return persistence.ErrOperationConflict
			}
			grant = existing.Grant
			return nil
		}

		if len(in.Recipients) == 0 || len(in.Recipients) > 100 {
			return errors.New("one to 100 named recipients required")
		}
		for _, p := range in.Recipients {
			if err := p.Validate(); err != nil {
				return err
			}
		}
		if !in.Reviewed || in.ReviewDigest == "" {
			return errors.New("explicit review of the preview is required")
		}
		if in.Mode != "snapshot" && in.Mode != "live" {
			return errors.New("sharing mode must be snapshot or live")
		}
		if in.Mode == "live" && !in.LiveUpdates {
			return errors.New("live mode requires consent to future edits of selected items")
		}
		if !in.ExpiresAt.After(s.now()) {
			return errors.New("expiry must be in the future")
		}
		preview, err := s.preview(PreviewRequest{Source: in.Source, Selection: in.Selection})
		if err != nil {
			return err
		}
		if preview.ReviewDigest != in.ReviewDigest {
			return errors.New("selected content changed; review a fresh preview")
		}
		if len(preview.Document.Items) == 0 {
			return errors.New("select at least one visible item")
		}
		_, items, err := Project(in.Source, in.Selection)
		if err != nil {
			return err
		}
		r := record{Grant: Grant{ID: id, SessionID: in.Source.SessionID, Mode: in.Mode, ExpiresAt: in.ExpiresAt}, Creator: in.Actor, Recipients: in.Recipients, Selection: in.Selection, OperationID: in.OperationID, IntentDigest: intentDigest}
		if in.Mode == "snapshot" {
			r.Frozen = Source{SessionID: in.Source.SessionID, Items: items}
		}
		grant = r.Grant
		return tx.Put(namespace, id, r)
	})
	return grant, err
}

func (s *Service) load(ctx context.Context, id string) (record, persistence.Revision, error) {
	if id == "" {
		return record{}, 0, errUnavailable
	}
	view, err := s.documents.Read(ctx, persistence.Query{Namespace: namespace, Keys: []string{id}})
	if err != nil {
		return record{}, 0, err
	}
	if len(view.Documents) != 1 {
		return record{}, view.Revision, errUnavailable
	}
	var r record
	if err := json.Unmarshal(view.Documents[0].Value, &r); err != nil {
		return r, view.Revision, err
	}
	if r.ID != id {
		return r, view.Revision, errUnavailable
	}
	return r, view.Revision, nil
}
func (s *Service) readable(ctx context.Context, in ReadRequest) (record, persistence.Revision, error) {
	r, revision, err := s.load(ctx, in.ShareID)
	if err != nil {
		return r, revision, err
	}
	if in.Actor.Validate() != nil || r.SessionID != in.SessionID || r.Revoked || !s.now().Before(r.ExpiresAt) {
		return r, revision, errUnavailable
	}
	for _, recipient := range r.Recipients {
		if recipient == in.Actor {
			return r, revision, nil
		}
	}
	if r.Creator == in.Actor {
		return r, revision, nil
	}
	return r, revision, errUnavailable
}
func (s *Service) source(r record, in ReadRequest) (Source, error) {
	if r.Mode == "snapshot" {
		return r.Frozen, nil
	}
	if in.Source == nil || in.Source.SessionID != r.SessionID {
		return Source{}, errors.New("live source required")
	}
	return *in.Source, nil
}
func (s *Service) read(ctx context.Context, in ReadRequest) (Document, error) {
	r, _, err := s.readable(ctx, in)
	if err != nil {
		return Document{}, err
	}
	source, err := s.source(r, in)
	if err != nil {
		return Document{}, err
	}
	out, _, err := Project(source, r.Selection)
	return out, err
}
func (s *Service) attachment(ctx context.Context, in AttachmentRequest) (AttachmentResult, error) {
	r, _, err := s.readable(ctx, in.ReadRequest)
	if err != nil {
		return AttachmentResult{}, err
	}
	source, err := s.source(r, in.ReadRequest)
	if err != nil {
		return AttachmentResult{}, err
	}
	document, items, err := Project(source, r.Selection)
	if err != nil {
		return AttachmentResult{}, err
	}
	for i, item := range items {
		if item.ID == in.AttachmentID && item.Kind == "attachment" {
			return AttachmentResult{Attachment: *document.Items[i].Attachment, Data: item.Data}, nil
		}
	}
	return AttachmentResult{}, errUnavailable
}
func (s *Service) revoke(ctx context.Context, in RevokeRequest) error {
	return persistence.Update(ctx, s.documents, func(tx *persistence.Tx) error {
		var r record
		found, err := tx.Get(namespace, in.ShareID, &r)
		if err != nil {
			return err
		}
		if !found || r.Creator != in.Actor || r.SessionID != in.SessionID {
			return errUnavailable
		}
		if r.Revoked {
			return nil
		}
		r.Revoked = true
		return tx.Put(namespace, in.ShareID, r)
	})
}

func (s *Service) Authorize(ctx context.Context, in access.Request) (access.Decision, error) {
	deny := access.Decision{Reason: "share-unavailable"}
	if in.Action != "session/share/read" && in.Action != "session/share/attachment/read" {
		return deny, nil
	}
	if err := in.Validate(); err != nil {
		return deny, nil
	}
	if len(in.Resources) != 1 || in.Resources[0].Kind != "session-share" {
		return deny, nil
	}
	r, _, err := s.load(ctx, in.Resources[0].ID)
	if err != nil {
		if errors.Is(err, errUnavailable) {
			return deny, nil
		}
		return deny, fmt.Errorf("read share policy: %w", err)
	}
	_, _, err = s.readable(ctx, ReadRequest{Actor: in.Principal, ShareID: r.ID, SessionID: r.SessionID})
	if err != nil {
		return deny, nil
	}
	return access.Decision{Allow: true}, nil
}
