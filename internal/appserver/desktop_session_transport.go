package appserver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

const desktopSessionDispatchMethod = "session/desktop/dispatch"
const desktopSessionExportMethod = "session/desktop/export"
const desktopSessionListMethod = "session/desktop/list"

// A key identifies one live host, never an owner grant. Only the local desktop
// coordinator may carry this identity from its authenticated source connection.
type desktopSessionIdentity struct {
	Version   int    `json:"version"`
	HostID    string `json:"host_id"`
	PublicKey string `json:"public_key"`
}
type desktopSessionExport struct {
	Version   int    `json:"version"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type desktopSessionImport struct {
	Export        desktopSessionExport   `json:"export"`
	TrustedSource desktopSessionIdentity `json:"trusted_source"`
}
type desktopSessionSource struct {
	HostID        string `json:"host_id"`
	PublicKey     string `json:"public_key"`
	UserSpaceID   string `json:"user_space_id"`
	ProductDomain string `json:"product_domain"`
	PluginID      string `json:"plugin_id"`
}
type desktopSessionImage struct {
	pluginprotocol.ModelImageV1
	SHA256 string `json:"sha256"`
}
type desktopSessionFile struct {
	pluginprotocol.ModelFileV1
	SHA256 string `json:"sha256"`
}
type desktopSessionMessage struct {
	Prompt string                `json:"prompt"`
	Images []desktopSessionImage `json:"images,omitempty"`
	Files  []desktopSessionFile  `json:"files,omitempty"`
	Digest string                `json:"digest"`
}
type desktopSessionPayload struct {
	Target  *desktopSessionIdentity        `json:"target,omitempty"`
	Version int                            `json:"version"`
	Source  desktopSessionSource           `json:"source"`
	Request pluginhost.SessionRouteRequest `json:"request"`
	Message *desktopSessionMessage         `json:"message,omitempty"`
}
type desktopSessionTicket struct {
	ctx        context.Context
	connection string
	provider   string
	export     desktopSessionExport
	scopes     []authorizedScope
}
type desktopSessionState struct {
	once     sync.Once
	identity desktopSessionIdentity
	key      ed25519.PrivateKey
	mu       sync.Mutex
	tickets  map[string]desktopSessionTicket
	call     func(context.Context, any) (json.RawMessage, error)
}
type desktopImportedMessageKey struct{}

func (s *Server) desktopSessionIdentity() desktopSessionIdentity {
	s.desktopSessions.once.Do(func() {
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		s.desktopSessions.key = private
		sum := sha256.Sum256(public)
		s.desktopSessions.identity = desktopSessionIdentity{Version: 1, HostID: hex.EncodeToString(sum[:16]), PublicKey: base64.StdEncoding.EncodeToString(public)}
		s.desktopSessions.tickets = make(map[string]desktopSessionTicket)
	})
	return s.desktopSessions.identity
}
func (s *Server) requireDesktopOwner(ctx context.Context) error {
	p, authenticated := principal.FromContext(ctx)
	if !authenticated || !p.IsOwner() || p.DurableAuthority() != "local-installation" || access.DelegationFromContext(ctx) != nil {
		return errors.New("desktop session bridge requires the authenticated local installation owner")
	}
	return p.Check(ctx)
}
func (s *Server) prepareDesktopSessionRoute(ctx context.Context, pluginID, provider string, request pluginhost.SessionRouteRequest) (pluginhost.SessionRouteRequest, func(), error) {
	if err := s.requireDesktopOwner(ctx); err != nil {
		return request, nil, err
	}
	connection, _, admissionErr := clientauthority.Connection(ctx, s.rt.UserBinding())
	if admissionErr != nil {
		return request, nil, admissionErr
	}
	if !s.rt.UserBinding().IsUser() {
		return request, nil, errors.New("desktop session source requires an explicit user host")
	}
	if request.TransportTicket != nil || pluginID == "" || provider == "" {
		return request, nil, errors.New("invalid desktop session ticket request")
	}
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.Locator.SessionID = strings.TrimSpace(request.Locator.SessionID)
	if err := request.Validate(); err != nil {
		return request, nil, err
	}
	if request.Operation == "create" || request.Operation == "list" && request.Locator.ExecutionHost != "desktop" || request.Operation != "list" && !isDesktopSessionTarget(request.Locator.ExecutionHost) {
		return request, nil, errors.New("desktop transport permits discovery and existing-session inspect/send/cancel only")
	}
	if err := s.AuthorizeResource(ctx, "session/desktop.route", access.Resource{Kind: "user-space", ID: s.rt.UserSpaceID}); err != nil {
		return request, nil, err
	}
	if request.Operation == "list" {
		var p pluginhost.SessionListParams
		if err := decodeParams(request.Params, &p); err != nil {
			return request, nil, err
		}
		if p.ExecutionAuthorization != nil || p.ParentSessionID != "" || p.Scope != "" && p.Scope != pluginhost.SessionListScopeShared {
			return request, nil, errors.New("desktop discovery supports shared sessions without source authority or parent filters")
		}
	}
	var scopes []authorizedScope
	identity := s.desktopSessionIdentity()
	payload := desktopSessionPayload{Version: 1, Source: desktopSessionSource{HostID: identity.HostID, PublicKey: identity.PublicKey, UserSpaceID: s.rt.UserSpaceID, ProductDomain: s.rt.ProductDomain, PluginID: pluginID}, Request: request}
	if request.Operation == "send" {
		var p pluginhost.SessionSendParams
		if err := decodeParams(request.Params, &p); err != nil {
			return request, nil, err
		}
		p.RequestID = strings.TrimSpace(p.RequestID)
		p.SessionID = strings.TrimSpace(p.SessionID)
		if p.RequestID != request.OperationID || p.SessionID != request.Locator.SessionID || p.Input.Handoff == nil || p.UserInputRequestID != "" || p.ResumeAccepted || p.ResumeReceiptID != "" || p.ExecutionAuthorization != nil || p.ControlRevision != 0 || p.ReplyToTurnID != "" || p.Relation != nil || len(p.Input.ContextBlocks) > 0 || len(p.Input.Attachments) > 0 || p.IfRunning != "" && p.IfRunning != pluginhost.SessionIfRunningQueue {
			return request, nil, errors.New("desktop send requires original sources and queue-only message parameters, not execution authority")
		}
		p.IfRunning = pluginhost.SessionIfRunningQueue
		binding, err := runtime.RetainProductInvocationState(ctx)
		if err != nil {
			return request, nil, err
		}
		defer binding.Close()
		prompt, images, files, err := s.resolveSessionHandoff(ctx, binding, pluginID, p)
		if err != nil {
			return request, nil, err
		}
		invocation, err := runtime.RetainInvocationState(ctx)
		if err != nil {
			return request, nil, err
		}
		defer invocation.Close()
		// Request IDs are plugin-wide in the source authority too. Pin the logical
		// destination, not a process generation, so reconnect may retry but retarget
		// cannot duplicate a send across two otherwise legitimate owner contexts.
		logical := request.Locator
		logical.DisplayHost = ""
		intentBytes, err := json.Marshal(struct {
			Locator pluginhost.SessionLocatorV1 `json:"locator"`
			Message string                      `json:"message"`
		}{logical, sessionHandoffDigest(p)})
		if err != nil {
			return request, nil, err
		}
		intent := sha256.Sum256(intentBytes)
		if err = recordPluginTurnOrigin(ctx, binding, pluginID, p.RequestID, pluginTurnOrigin{
			Composition: invocation.ExecutionCompositionID(), Session: logical.ExecutionHost + "/" + logical.SessionID, HandoffDigest: hex.EncodeToString(intent[:]),
		}); err != nil {
			return request, nil, err
		}
		// Preserve the source locator in the consumer-visible material, independently
		// of the source's synthesized instruction and the transport signature.
		var envelope sessionHandoffEnvelope
		if err = json.Unmarshal([]byte(prompt), &envelope); err != nil {
			return request, nil, err
		}
		for _, material := range append(append([]sessionHandoffMessage(nil), envelope.OriginalMessages...), envelope.ReplyContext...) {
			resources := []access.Resource{{Kind: "user-space", ID: s.rt.UserSpaceID}, {Kind: "session", ID: material.Source.SessionID}}
			scopes = append(scopes, authorizedScope{action: "user/session/history.read", resources: resources})
			if len(material.Attachments) > 0 || len(material.ToolResult) > 0 {
				scopes = append(scopes, authorizedScope{action: "user/session/attachment.read", resources: resources})
			}
		}
		envelope.SourceHost = &payload.Source
		raw, _ := json.Marshal(envelope)
		payload.Message = &desktopSessionMessage{Prompt: string(raw), Digest: sessionHandoffDigest(p)}
		for _, value := range images {
			bytes, _ := base64.StdEncoding.DecodeString(value.Data)
			sum := sha256.Sum256(bytes)
			payload.Message.Images = append(payload.Message.Images, desktopSessionImage{ModelImageV1: modelwire.InputImageToWire(value), SHA256: hex.EncodeToString(sum[:])})
		}
		for _, value := range files {
			bytes, _ := base64.StdEncoding.DecodeString(value.Data)
			sum := sha256.Sum256(bytes)
			payload.Message.Files = append(payload.Message.Files, desktopSessionFile{ModelFileV1: modelwire.InputFileToWire(value), SHA256: hex.EncodeToString(sum[:])})
		}
		// Grants remain at the source; the target receives resolved immutable bytes.
		p.Input.Handoff = nil
		p.Input.Prompt = ""
		p.Presentation = nil
		payload.Request.Params, _ = json.Marshal(p)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return request, nil, err
	}
	if len(raw) > 32*1024*1024 {
		return request, nil, errors.New("desktop handoff exceeds the transfer limit")
	}
	export := desktopSessionExport{Version: 1, Payload: string(raw), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(s.desktopSessions.key, raw))}
	idBytes := make([]byte, 32)
	if _, err = rand.Read(idBytes); err != nil {
		return request, nil, err
	}
	ticketID := hex.EncodeToString(idBytes)
	s.desktopSessions.mu.Lock()
	if len(s.desktopSessions.tickets) >= 64 {
		s.desktopSessions.mu.Unlock()
		return request, nil, errors.New("too many desktop session handoffs in flight")
	}
	s.desktopSessions.tickets[ticketID] = desktopSessionTicket{ctx: ctx, connection: connection, provider: provider, export: export, scopes: scopes}
	s.desktopSessions.mu.Unlock()
	release := func() {
		s.desktopSessions.mu.Lock()
		delete(s.desktopSessions.tickets, ticketID)
		s.desktopSessions.mu.Unlock()
	}
	request.TransportTicket = &pluginhost.SessionTransportTicket{ID: ticketID, SourceHost: identity.HostID}
	return request, release, nil
}
func isDesktopSessionTarget(host string) bool {
	return strings.HasPrefix(host, "desktop:") || strings.HasPrefix(host, "desktop-project:") || strings.HasPrefix(host, "desktop-context:")
}
func (s *Server) exportDesktopSession(ctx context.Context, id string, targets ...*desktopSessionIdentity) (desktopSessionExport, error) {
	if err := s.requireDesktopOwner(ctx); err != nil {
		return desktopSessionExport{}, err
	}
	s.desktopSessions.mu.Lock()
	ticket, ok := s.desktopSessions.tickets[id]
	s.desktopSessions.mu.Unlock()
	if !ok {
		return desktopSessionExport{}, errors.New("desktop handoff ticket expired or unavailable")
	}
	connection, _, err := clientauthority.Connection(ctx, s.rt.UserBinding())
	if err != nil || connection != ticket.connection {
		return desktopSessionExport{}, errors.New("desktop ticket belongs to another admitted connection")
	}
	if err := s.requireDesktopOwner(ticket.ctx); err != nil {
		return desktopSessionExport{}, err
	}
	for _, scope := range ticket.scopes {
		if err := s.AuthorizeResource(ticket.ctx, scope.action, scope.resources...); err != nil {
			return desktopSessionExport{}, err
		}
	}
	if len(targets) > 0 && targets[0] != nil {
		target := targets[0]
		key, err := base64.StdEncoding.DecodeString(target.PublicKey)
		if err != nil || target.Version != 1 || target.HostID == "" || len(key) != ed25519.PublicKeySize {
			return desktopSessionExport{}, errors.New("invalid resolved desktop target")
		}
		var payload desktopSessionPayload
		if err = json.Unmarshal([]byte(ticket.export.Payload), &payload); err != nil {
			return desktopSessionExport{}, err
		}
		if payload.Request.Operation == "list" {
			return desktopSessionExport{}, errors.New("discovery ticket cannot bind a target")
		}
		if payload.Target != nil && *payload.Target != *target {
			return desktopSessionExport{}, errors.New("desktop ticket already bound to a different target generation")
		}
		payload.Target = target
		raw, err := json.Marshal(payload)
		if err != nil {
			return desktopSessionExport{}, err
		}
		ticket.export = desktopSessionExport{Version: 1, Payload: string(raw), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(s.desktopSessions.key, raw))}
		s.desktopSessions.mu.Lock()
		current, exists := s.desktopSessions.tickets[id]
		if !exists {
			s.desktopSessions.mu.Unlock()
			return desktopSessionExport{}, errors.New("desktop ticket expired")
		}
		// Concurrent exporters cannot retarget a ticket after its first binding.
		var currentPayload desktopSessionPayload
		_ = json.Unmarshal([]byte(current.export.Payload), &currentPayload)
		if currentPayload.Target != nil && *currentPayload.Target != *target {
			s.desktopSessions.mu.Unlock()
			return desktopSessionExport{}, errors.New("desktop ticket target conflict")
		}
		s.desktopSessions.tickets[id] = ticket
		s.desktopSessions.mu.Unlock()
	}
	return ticket.export, nil
}
func (s *Server) dispatchDesktopTicket(ctx context.Context, provider, id string) (json.RawMessage, error) {
	s.desktopSessions.mu.Lock()
	ticket, ok := s.desktopSessions.tickets[id]
	call := s.desktopSessions.call
	s.desktopSessions.mu.Unlock()
	if !ok || ticket.provider != provider {
		return nil, errors.New("desktop ticket is not bound to this selected transport provider")
	}
	if err := s.requireDesktopOwner(ticket.ctx); err != nil {
		return nil, err
	}
	if call == nil {
		return nil, errors.New("local desktop session coordinator is unavailable")
	}
	// The invocation's cancellation revokes the ticket. Never switch connection or
	// replay automatically after an ambiguous transport failure.
	callCtx, cancel := context.WithCancel(ticket.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return call(callCtx, map[string]any{"version": 1, "ticket_id": id, "source_host": s.desktopSessionIdentity().HostID})
}

func (s *Server) dispatchDesktopSession(ctx context.Context, input desktopSessionImport) (pluginhost.SessionRouteResult, error) {
	if err := s.requireDesktopOwner(ctx); err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	if s.rt.UserBinding().IsUser() {
		return pluginhost.SessionRouteResult{}, errors.New("desktop targets must be local Coding hosts")
	}
	if input.Export.Version != 1 || input.TrustedSource.Version != 1 || len(input.Export.Payload) > 32*1024*1024 {
		return pluginhost.SessionRouteResult{}, errors.New("invalid desktop export version or size")
	}
	key, err := base64.StdEncoding.DecodeString(input.TrustedSource.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return pluginhost.SessionRouteResult{}, errors.New("invalid authenticated source key")
	}
	signature, err := base64.StdEncoding.DecodeString(input.Export.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), []byte(input.Export.Payload), signature) {
		return pluginhost.SessionRouteResult{}, errors.New("desktop handoff signature mismatch")
	}
	var payload desktopSessionPayload
	if err = decodeParams(json.RawMessage(input.Export.Payload), &payload); err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	if payload.Version != 1 || payload.Source.HostID != input.TrustedSource.HostID || payload.Source.PublicKey != input.TrustedSource.PublicKey || payload.Source.PluginID == "" || (userspace.Binding{UserSpaceID: payload.Source.UserSpaceID, ProductDomain: payload.Source.ProductDomain}).Validate() != nil || payload.Source.UserSpaceID == "" {
		return pluginhost.SessionRouteResult{}, errors.New("desktop handoff source does not match the authenticated connection")
	}
	request := payload.Request
	if err = request.Validate(); err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	if request.TransportTicket != nil || !isDesktopSessionTarget(request.Locator.ExecutionHost) || payload.Target == nil || *payload.Target != s.desktopSessionIdentity() || request.Operation == "list" || request.Operation == "create" {
		return pluginhost.SessionRouteResult{}, errors.New("desktop handoff target host changed or operation is unavailable")
	}
	if err = s.AuthorizeResource(ctx, "session/desktop."+request.Operation, access.Resource{Kind: "session", ID: request.Locator.SessionID}); err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	binding, err := runtime.RetainProductInvocationState(ctx)
	if err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	defer binding.Close()
	meta, found, err := binding.Sessions.Find(request.Locator.SessionID)
	if err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	if !found || meta.Visibility == pluginhost.SessionVisibilityPlugin || meta.ArchivedAt != nil || !s.rt.UserBinding().Matches(userspace.Binding{UserSpaceID: meta.UserSpaceID, ProductDomain: meta.ProductDomain}) {
		return pluginhost.SessionRouteResult{}, errors.New("desktop target is not an active shared session in this host")
	}
	// Stable owner-scoped provenance survives reconnects. A source host's transient
	// key never becomes a target plugin's identity or a permission grant.
	namespace := sha256.Sum256([]byte(payload.Source.UserSpaceID + "\x00" + payload.Source.ProductDomain + "\x00" + payload.Source.PluginID))
	pluginID := "desktop-source-" + hex.EncodeToString(namespace[:16])
	var result any
	switch request.Operation {
	case "send":
		var p pluginhost.SessionSendParams
		if err = decodeParams(request.Params, &p); err != nil {
			return pluginhost.SessionRouteResult{}, err
		}
		if payload.Message == nil || payload.Message.Prompt == "" || payload.Message.Digest == "" || p.RequestID != request.OperationID || p.SessionID != request.Locator.SessionID || p.UserInputRequestID != "" || p.ResumeAccepted || p.ResumeReceiptID != "" || p.ExecutionAuthorization != nil || p.ControlRevision != 0 || p.ReplyToTurnID != "" || p.Relation != nil || p.Presentation != nil || p.Input.Handoff != nil || p.Input.Prompt != "" || len(p.Input.ContextBlocks) > 0 || len(p.Input.Attachments) > 0 || p.IfRunning != "" && p.IfRunning != pluginhost.SessionIfRunningQueue {
			return pluginhost.SessionRouteResult{}, errors.New("desktop message cannot import execution authority or alter destination policy")
		}
		total := 0
		for _, image := range payload.Message.Images {
			if err = verifyDesktopMaterial(image.Data, image.SHA256, &total); err != nil {
				return pluginhost.SessionRouteResult{}, err
			}
		}
		for _, file := range payload.Message.Files {
			if err = verifyDesktopMaterial(file.Data, file.SHA256, &total); err != nil {
				return pluginhost.SessionRouteResult{}, err
			}
		}
		if len(payload.Message.Images)+len(payload.Message.Files) > 16 {
			return pluginhost.SessionRouteResult{}, errors.New("desktop transfer exceeds 16 attachments")
		}
		p.Input.Prompt = payload.Message.Prompt
		p.IfRunning = pluginhost.SessionIfRunningQueue
		ctx = context.WithValue(ctx, desktopImportedMessageKey{}, payload.Message)
		result, err = s.sendPluginSession(ctx, pluginID, p)
	case "inspect":
		var p pluginhost.SessionInspectParams
		if err = decodeParams(request.Params, &p); err != nil {
			return pluginhost.SessionRouteResult{}, err
		}
		if p.SessionID != request.Locator.SessionID || p.ExecutionAuthorization != nil || p.TurnID != "" || p.Wait != "" && p.Wait != pluginhost.SessionInspectWaitNone {
			return pluginhost.SessionRouteResult{}, errors.New("desktop inspect requires its exact session and optional owned request")
		}
		if p.RequestID == "" {
			var list pluginhost.SessionListResult
			list, err = s.listPluginSessions(ctx, pluginID, pluginhost.SessionListParams{Scope: pluginhost.SessionListScopeShared})
			for _, item := range list.Sessions {
				if item.SessionID == p.SessionID {
					result = map[string]any{"session": item}
					break
				}
			}
			if err == nil && result == nil {
				err = errors.New("desktop session is unavailable")
			}
		} else {
			result, err = s.inspectPluginSession(ctx, pluginID, p)
		}
	case "cancel":
		var p pluginhost.SessionCancelParams
		if err = decodeParams(request.Params, &p); err != nil {
			return pluginhost.SessionRouteResult{}, err
		}
		if p.SessionID != request.Locator.SessionID || p.ExecutionAuthorization != nil || p.ControlRevision != 0 || (p.TurnID == "") == (p.QueueID == "") {
			return pluginhost.SessionRouteResult{}, errors.New("desktop cancel requires one exact owned turn or queue")
		}
		result, err = s.cancelPluginSession(ctx, pluginID, p)
	default:
		return pluginhost.SessionRouteResult{}, errors.New("unsupported desktop session operation")
	}
	if err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	raw, err := json.Marshal(result)
	return pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: request.Locator, Status: "completed", Result: raw}, err
}

type desktopSessionSummary struct {
	pluginhost.SessionSummary
	Locator pluginhost.SessionLocatorV1 `json:"locator"`
}

type desktopSessionListResult struct {
	Sessions []desktopSessionSummary `json:"sessions"`
}

func (s *Server) listDesktopSessions(ctx context.Context) (*desktopSessionListResult, error) {
	if err := s.requireDesktopOwner(ctx); err != nil {
		return nil, err
	}
	if s.rt.UserBinding().IsUser() {
		return nil, errors.New("desktop targets must be Coding hosts")
	}
	resource, resourceErr := resolvedWorkspaceResource(s.rt.RootDir)
	if resourceErr != nil {
		return nil, resourceErr
	}
	if err := s.AuthorizeResource(ctx, "session/desktop.list", resource); err != nil {
		return nil, err
	}
	list, err := s.listPluginSessions(ctx, "desktop-discovery", pluginhost.SessionListParams{Scope: pluginhost.SessionListScopeShared})
	if err != nil {
		return nil, err
	}
	result := &desktopSessionListResult{Sessions: []desktopSessionSummary{}}
	for _, item := range list.Sessions {
		if item.UserSpaceID != "" || item.ProductDomain != "" {
			continue
		}
		if err = s.AuthorizeResource(ctx, "session/desktop.inspect", access.Resource{Kind: "session", ID: item.SessionID}); err != nil {
			return nil, err
		}
		result.Sessions = append(result.Sessions, desktopSessionSummary{SessionSummary: item, Locator: pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: "desktop:" + s.desktopSessionIdentity().HostID, SessionID: item.SessionID}})
	}
	return result, nil
}
func (s *Server) handleDesktopSession(ctx context.Context, req Request) error {
	if err := s.requireDesktopOwner(ctx); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// The endpoint is reserved to the local main-process coordinator; its generic
	// renderer and remote RPC gateways reject this namespace before forwarding.
	resource := access.Resource{Kind: "user-space", ID: s.rt.UserSpaceID}
	if !s.rt.UserBinding().IsUser() {
		var resourceErr error
		resource, resourceErr = resolvedWorkspaceResource(s.rt.RootDir)
		if resourceErr != nil {
			return s.writeResponse(req.ID, nil, resourceErr)
		}
	}
	if err := s.RegisterAuthorizedResponse(ctx, req, req.Method, false, resource); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	ctx = runtime.WithInvocationState(ctx, binding)
	var result any
	switch req.Method {
	case desktopSessionExportMethod:
		var input struct {
			TicketID string                  `json:"ticket_id"`
			Target   *desktopSessionIdentity `json:"target,omitempty"`
		}
		if err = decodeParams(req.Params, &input); err == nil {
			result, err = s.exportDesktopSession(ctx, input.TicketID, input.Target)
			if err == nil {
				s.desktopSessions.mu.Lock()
				ticket, found := s.desktopSessions.tickets[input.TicketID]
				s.desktopSessions.mu.Unlock()
				if !found {
					err = errors.New("desktop ticket expired")
				} else {
					for _, scope := range ticket.scopes {
						if err = s.AppendAuthorizedResponseGuard(req, scope.action, false, scope.resources...); err != nil {
							break
						}
					}
				}
			}
		}
	case desktopSessionListMethod:
		var input struct{}
		if err = decodeParams(req.Params, &input); err == nil {
			var list *desktopSessionListResult
			list, err = s.listDesktopSessions(ctx)
			result = list
			if err == nil {
				for _, item := range list.Sessions {
					if err = s.AppendAuthorizedResponseGuard(req, "session/desktop.inspect", false, access.Resource{Kind: "session", ID: item.SessionID}); err != nil {
						break
					}
				}
			}
		}
	case desktopSessionDispatchMethod:
		var input desktopSessionImport
		if err = decodeParams(req.Params, &input); err == nil {
			result, err = s.dispatchDesktopSession(ctx, input)
			if err == nil {
				var payload desktopSessionPayload
				_ = json.Unmarshal([]byte(input.Export.Payload), &payload)
				err = s.AppendAuthorizedResponseGuard(req, "session/desktop."+payload.Request.Operation, false, access.Resource{Kind: "session", ID: payload.Request.Locator.SessionID})
			}
		}
	default:
		err = fmt.Errorf("unknown desktop session method")
	}
	return s.writeResponse(req.ID, result, err)
}

func verifyDesktopMaterial(data, digest string, total *int) error {
	bytes, err := base64.StdEncoding.DecodeString(data)
	*total += len(bytes)
	sum := sha256.Sum256(bytes)
	if err != nil || len(bytes) == 0 || *total > runtime.MaxClientInputAttachmentBytes || hex.EncodeToString(sum[:]) != digest {
		return errors.New("desktop attachment is missing, corrupt or exceeds size limit")
	}
	return nil
}
