package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// A provider URI is opaque location metadata, not authorization. Every request
// must still name a published result in an authorized source conversation.
type artifactReadParams struct {
	ThreadID     string `json:"thread_id"`
	URI          string `json:"uri"`
	SHA256       string `json:"sha256"`
	GenerationID string `json:"generation_id"`
	RequestID    string `json:"request_id"`
	Offset       int64  `json:"offset"`
	MaxBytes     int64  `json:"max_bytes"`
}
type artifactOperationKey struct {
	principal principal.Identity
	requestID string
}
type artifactReadOperation struct {
	threadID string
	cancel   context.CancelFunc
}

func (s *Server) cancelArtifactReads() {
	s.artifactReads.Range(func(_, value any) bool { value.(*artifactReadOperation).cancel(); return true })
}

func (s *Server) handleThreadArtifactRead(ctx context.Context, req Request) error {
	var params artifactReadParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.ThreadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("invalid artifact session"))
	}
	key := artifactOperationKey{principal: s.Principal(ctx).Identity(), requestID: params.RequestID}
	if req.Method == "thread/artifact/cancel" {
		if params.RequestID == "" {
			return s.writeResponse(req.ID, nil, errors.New("invalid artifact request"))
		}
		if operation, found := s.artifactReads.Load(key); found && operation.(*artifactReadOperation).threadID == params.ThreadID {
			operation.(*artifactReadOperation).cancel()
		}
		return s.writeResponse(req.ID, OKResult{OK: true}, nil)
	}
	if params.URI == "" || len(params.SHA256) != 64 {
		return s.writeResponse(req.ID, nil, errors.New("invalid artifact reference"))
	}
	if params.RequestID == "" || len(params.RequestID) > 256 {
		return s.writeResponse(req.ID, nil, errors.New("artifact operation requires a request identity"))
	}
	ctx, cancel := context.WithCancel(ctx)
	operation := &artifactReadOperation{threadID: params.ThreadID, cancel: cancel}
	if _, exists := s.artifactReads.LoadOrStore(key, operation); exists {
		cancel()
		return s.writeResponse(req.ID, nil, errors.New("artifact request is already active"))
	}
	// Publish cancellation ownership before allowing the next transport frame.
	if !s.startBackground(func() {
		defer cancel()
		defer s.artifactReads.Delete(key)
		_ = s.readThreadArtifact(ctx, req, params)
	}) {
		s.artifactReads.Delete(key)
		cancel()
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	return nil
}

func (s *Server) readThreadArtifact(ctx context.Context, req Request, params artifactReadParams) error {
	binding, _, err := s.retainUserThreadExecutionState(params.ThreadID, true)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	services, err := binding.Services()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if req.Method == "thread/artifact/read" && (params.GenerationID == "" || params.GenerationID != services.GenerationID) {
		return s.writeResponse(req.ID, nil, errors.New("artifact provider generation changed; reacquire the source"))
	}
	th, err := s.historyThread(binding, params.ThreadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var published pluginhost.ArtifactImportResult
	th.mu.Lock()
	for _, turn := range th.Turns {
		for _, item := range turn.Items {
			if item.ResultDetail == nil {
				continue
			}
			for _, part := range item.ResultDetail.Content {
				if part.URI == params.URI && part.Artifact != nil && part.Artifact.SHA256 == params.SHA256 && part.Artifact.SizeBytes != nil {
					published = pluginhost.ArtifactImportResult{ID: part.Artifact.Ref, URI: part.URI, Name: part.Name, MIMEType: part.MIMEType, Size: *part.Artifact.SizeBytes, SHA256: part.Artifact.SHA256}
				}
			}
		}
	}
	th.mu.Unlock()
	if published.ID == "" {
		return s.writeResponse(req.ID, nil, errors.New("artifact is not published in the source conversation"))
	}
	scope, err := binding.Host.OpenServiceScope(ctx, runtime.StateServicesConsumer, runtime.ArtifactServiceName)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer scope.Close()
	ctx = scope.Context
	artifact, managed, err := services.Artifacts.Resolve(ctx, params.ThreadID, params.URI)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if !managed || artifact != published {
		return s.writeResponse(req.ID, nil, errors.New("artifact identity changed"))
	}
	if req.Method == "thread/artifact/resolve" {
		return s.writeResponse(req.ID, struct {
			Artifact     pluginhost.ArtifactImportResult `json:"artifact"`
			GenerationID string                          `json:"generation_id"`
		}{artifact, services.GenerationID}, nil)
	}
	result, err := services.Artifacts.ReadRange(ctx, runtime.ArtifactRangeRequest{ArtifactReadRequest: runtime.ArtifactReadRequest{ArtifactResolveRequest: runtime.ArtifactResolveRequest{ThreadID: params.ThreadID, URI: params.URI}, MaxBytes: params.MaxBytes}, Offset: params.Offset, SHA256: params.SHA256})
	if err == nil && (result.Total != artifact.Size || result.MIMEType != artifact.MIMEType) {
		err = errors.New("artifact range identity changed")
	}
	return s.writeResponse(req.ID, result, err)
}
