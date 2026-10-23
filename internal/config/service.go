package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const ServiceName = "state.config"
const Module = "wuu.config.local"
const maxConfigSourceBytes = 8 << 20

// Source remains byte-for-byte intact for editing/export. Config is the
// effective, validated user configuration after restricted project overlays.
// Neither field is suitable for diagnostics because it may contain secrets.
type SourceView struct {
	Source          []byte `json:"source"`
	Revision        string `json:"revision"`
	Config          Config `json:"config"`
	Committed       bool   `json:"committed,omitempty"`
	ActivationError string `json:"activation_error,omitempty"`
}
type CompareExchangeSource struct {
	ExpectedRevision string `json:"expected_revision"`
	Source           []byte `json:"source"`
}

// Service owns a selected configuration file. Bootstrap may read composition
// identity before constructing the graph; ordinary settings use this service.
// An empty project root selects explicit-path semantics without project layers.
type Service struct {
	mu                sync.RWMutex
	id, path, project string
	state             pluginhost.State
	memory            *SourceView
	initializationErr error
	trustedProject    bool
}

func NewService(id, path, projectRoot string) *Service {
	return &Service{id: id, path: path, project: projectRoot, state: pluginhost.StatePrepared}
}

// NewProjectService explicitly trusts the project's standalone base and its
// complete settings layers. Only a missing primary permits the fallback file.
func NewProjectService(id, projectRoot string) *Service {
	return &Service{id: id, path: filepath.Join(projectRoot, localPrimaryConfig), project: projectRoot, trustedProject: true, state: pluginhost.StatePrepared}
}

// NewMemoryService selects caller-supplied embedded configuration without
// consulting files or applying defaults to the initial values.
func NewMemoryService(id string, cfg Config) *Service {
	s := &Service{id: id, state: pluginhost.StatePrepared, memory: &SourceView{}}
	s.memory.Source, s.initializationErr = json.Marshal(cfg)
	if s.initializationErr == nil {
		s.initializationErr = json.Unmarshal(s.memory.Source, &s.memory.Config)
	}
	s.memory.Revision = sourceRevision(s.memory.Source)
	return s
}
func (s *Service) ID() string { return s.id }
func (s *Service) Status() pluginhost.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginhost.Status{ID: s.id, State: s.state}
}
func (*Service) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*Service) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: ServiceName, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: "read", InputSchema: "state.config.read.input.v1", OutputSchema: "state.config.read.output.v1", Preparation: true},
		{Name: "compare-exchange", InputSchema: "state.config.compare-exchange.input.v1", OutputSchema: "state.config.compare-exchange.output.v1"},
	}}}
}
func (s *Service) Activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != pluginhost.StatePrepared || s.initializationErr != nil || (s.memory == nil && !filepath.IsAbs(s.path)) {
		return configServiceError("service_unavailable", "configuration provider could not be activated")
	}
	s.state = pluginhost.StateActive
	return nil
}
func (s *Service) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = pluginhost.StateStopped
	return nil
}
func (s *Service) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.state != pluginhost.StateActive && !(s.state == pluginhost.StatePrepared && params.Method == "read")) || s.initializationErr != nil || params.Service != ServiceName {
		return nil, configServiceError("service_unavailable", "configuration provider is unavailable")
	}
	var replacement *CompareExchangeSource
	var replacementConfig Config
	switch params.Method {
	case "read":
		if err := decodeConfigServiceRequest(params.Params, new(struct{})); err != nil {
			return nil, err
		}
	case "compare-exchange":
		replacement = &CompareExchangeSource{}
		if err := decodeConfigServiceRequest(params.Params, replacement); err != nil {
			return nil, err
		}
		var err error
		replacementConfig, err = s.decodeSource(replacement.Source)
		if err != nil {
			return nil, configServiceError("invalid_params", "replacement configuration is invalid")
		}
	default:
		return nil, configServiceError("method_not_found", "configuration method is unavailable")
	}
	if s.memory != nil {
		if replacement != nil {
			if replacement.ExpectedRevision != s.memory.Revision {
				return nil, configServiceError("config_conflict", "configuration changed")
			}
			s.memory = &SourceView{Source: replacement.Source, Revision: sourceRevision(replacement.Source), Config: replacementConfig}
		}
		view := *s.memory
		view.Committed = replacement != nil
		return json.Marshal(view)
	}
	lock, err := storelock.AcquireContext(ctx, filepath.Dir(s.path))
	if err != nil {
		return nil, configServiceError("config_unavailable", "configuration lock is unavailable")
	}
	defer lock.Release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sourcePath := s.path
	source, err := os.ReadFile(sourcePath)
	if errors.Is(err, os.ErrNotExist) && s.trustedProject {
		sourcePath = filepath.Join(s.project, localFallbackConfig)
		source, err = os.ReadFile(sourcePath)
	}
	if errors.Is(err, os.ErrNotExist) && !s.trustedProject {
		source, err = nil, nil
	}
	if err != nil {
		return nil, configServiceError("config_unavailable", "configuration source is unavailable")
	}
	if replacement != nil {
		if replacement.ExpectedRevision != s.revision(sourcePath, source) {
			return nil, configServiceError("config_conflict", "configuration changed")
		}
		if s.trustedProject {
			if _, err := s.effectiveConfig(sourcePath, replacement.Source); err != nil {
				return nil, configServiceError("invalid_params", "replacement configuration is invalid")
			}
		}
		if err := securefs.WriteFileAtomic(sourcePath, replacement.Source); err != nil {
			return nil, configServiceError("config_unavailable", "configuration write failed; it may have been applied")
		}
		source = replacement.Source
	}
	cfg, err := s.effectiveConfig(sourcePath, source)
	if err != nil {
		if replacement != nil {
			return json.Marshal(SourceView{Source: source, Revision: s.revision(sourcePath, source), Committed: true, ActivationError: "project_overlay_invalid"})
		}
		return nil, configServiceError("config_invalid", "configuration source or project overlay is invalid")
	}
	return json.Marshal(SourceView{Source: source, Revision: s.revision(sourcePath, source), Config: cfg, Committed: replacement != nil})
}

func (s *Service) revision(path string, source []byte) string {
	if !s.trustedProject {
		return sourceRevision(source)
	}
	digest := sha256.New()
	digest.Write([]byte(path))
	digest.Write([]byte{0})
	digest.Write(source)
	return hex.EncodeToString(digest.Sum(nil))
}

func (s *Service) effectiveConfig(path string, source []byte) (Config, error) {
	cfg, err := s.decodeSource(source)
	if err != nil || s.project == "" {
		return cfg, err
	}
	if s.trustedProject {
		cfg, _, err = applySettingsLayersSource(path, source, cfg, projectSettingsLayers(s.project))
	} else {
		cfg, _, err = applyRestrictedProjectLayers(path, s.project, cfg)
	}
	if err != nil {
		return Config{}, err
	}
	applyMCPJsonServers(&cfg, s.project)
	applyDefaults(&cfg)
	return cfg, cfg.Validate()
}

func (s *Service) decodeSource(source []byte) (Config, error) {
	if len(source) == 0 && !s.trustedProject {
		return Default(), nil
	}
	if len(source) > maxConfigSourceBytes || !json.Valid(source) {
		return Config{}, errors.New("invalid configuration source")
	}
	cfg, err := decodeConfig(source, "selected source")
	if err != nil {
		return Config{}, err
	}
	if s.trustedProject {
		return cfg, nil
	}
	applyDefaults(&cfg)
	return cfg, cfg.Validate()
}
func sourceRevision(source []byte) string {
	digest := sha256.Sum256(source)
	return hex.EncodeToString(digest[:])
}
func configServiceError(code, message string) error {
	return &pluginhost.HostServiceError{Code: code, Message: message}
}
func decodeConfigServiceRequest(raw json.RawMessage, target any) error {
	if len(raw) > 2*maxConfigSourceBytes {
		return configServiceError("invalid_params", "configuration request exceeds the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return configServiceError("invalid_params", "configuration request is invalid")
	}
	return nil
}

// Client captures the selected graph, and never retries uncertain writes.
type Client struct {
	call func(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func NewClient(call func(context.Context, string, json.RawMessage) (json.RawMessage, error)) *Client {
	return &Client{call: call}
}
func (c *Client) Read(ctx context.Context) (SourceView, error) {
	return c.invoke(ctx, "read", struct{}{})
}
func (c *Client) CompareExchange(ctx context.Context, revision string, source []byte) (SourceView, error) {
	return c.invoke(ctx, "compare-exchange", CompareExchangeSource{ExpectedRevision: revision, Source: source})
}
func (c *Client) invoke(ctx context.Context, method string, input any) (SourceView, error) {
	if c == nil || c.call == nil {
		return SourceView{}, configServiceError("service_unavailable", "configuration binding is unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return SourceView{}, configServiceError("invalid_params", "configuration request is invalid")
	}
	defer clear(raw)
	result, err := c.call(ctx, method, raw)
	defer clear(result)
	if err != nil {
		var serviceErr *pluginhost.HostServiceError
		if errors.As(err, &serviceErr) {
			return SourceView{}, configServiceError(serviceErr.Code, "selected configuration provider failed")
		}
		return SourceView{}, configServiceError("config_unavailable", "selected configuration provider failed; a write may have been applied")
	}
	var view SourceView
	if json.Unmarshal(result, &view) != nil {
		return SourceView{}, configServiceError("invalid_response", "configuration provider returned an invalid response")
	}
	return view, nil
}
