package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/chatgpt"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/siwc"
)

type chatGPTBinding struct {
	services *runtime.StateServices
	manager  *siwc.Manager
	release  func()
	cancel   context.CancelFunc
	done     chan struct{}
	polls    chan chan AuthChatGPTLoginResult
	result   AuthChatGPTLoginResult
	expires  time.Time
}

// Ordinary model and configuration operations own a finite generation capture.
// Only an asynchronous login transfers that capture to its completion worker.
func (s *Server) modelServices() (*runtime.StateServices, func(), error) {
	s.chatGPTMu.Lock()
	closed, injected := s.closed.Load(), s.chatGPTManager
	s.chatGPTMu.Unlock()
	if closed {
		return nil, nil, errServerClosed
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return nil, nil, err
	}
	if injected != nil {
		services.ChatGPT = injected
	}
	return services, release, nil
}

func (s *Server) buildStreamClientWithServices(services *runtime.StateServices, provider config.ProviderConfig, name string) (providers.StreamClient, error) {
	if strings.EqualFold(strings.TrimSpace(provider.Type), "chatgpt") {
		manager := services.ChatGPT
		if manager == nil {
			return nil, errors.New("selected ChatGPT credential manager is unavailable")
		}
		client, err := chatgpt.New(chatgpt.ClientConfig{AccountID: provider.ChatGPTAccountID, AccessToken: manager.AccessToken, BindRequest: manager.BindRequest, HTTPClient: s.chatGPTHTTP})
		if err != nil {
			return nil, err
		}
		return providers.AdaptStreamClient(client), nil
	}
	return providerfactory.BuildStreamClient(provider, name, providerfactory.Bindings{Credentials: services.Credentials, ChatGPT: services.ChatGPT})
}

// A login entry keeps only its terminal receipt after releasing the generation.
// Polls ask the owner goroutine for a fresh status, so a callback completed just
// before polling is observed without waiting for the background check interval.
func (b *chatGPTBinding) poll(ctx context.Context) (AuthChatGPTLoginResult, error) {
	reply := make(chan AuthChatGPTLoginResult, 1)
	select {
	case <-b.done:
		return b.result, nil
	case <-ctx.Done():
		return AuthChatGPTLoginResult{}, ctx.Err()
	case b.polls <- reply:
	}
	select {
	case result := <-reply:
		return result, nil
	case <-b.done:
		return b.result, nil
	case <-ctx.Done():
		return AuthChatGPTLoginResult{}, ctx.Err()
	}
}

func (s *Server) retainChatGPTLogin(services *runtime.StateServices, release func(), attempt siwc.Attempt) (*chatGPTBinding, error) {
	ctx, cancel := context.WithCancel(context.Background())
	binding := &chatGPTBinding{services: services, manager: services.ChatGPT, release: release, cancel: cancel, done: make(chan struct{}), polls: make(chan chan AuthChatGPTLoginResult), expires: attempt.ExpiresAt}
	s.chatGPTMu.Lock()
	if s.closed.Load() {
		s.chatGPTMu.Unlock()
		cancel()
		return nil, errServerClosed
	}
	if s.chatGPTBindings == nil {
		s.chatGPTBindings = make(map[string]*chatGPTBinding)
	}
	for id, previous := range s.chatGPTBindings {
		select {
		case <-previous.done:
			if !time.Now().Before(previous.expires) {
				delete(s.chatGPTBindings, id)
			}
		default:
		}
	}
	s.chatGPTBindings[attempt.ID] = binding
	s.chatGPTMu.Unlock()
	go s.finishChatGPTLogin(ctx, binding, attempt.ID)
	return binding, nil
}

func (s *Server) finishChatGPTLogin(ctx context.Context, binding *chatGPTBinding, id string) {
	defer close(binding.done)
	defer func() {
		binding.release()
		s.chatGPTMu.Lock()
		binding.manager, binding.services, binding.release = nil, nil, nil
		s.chatGPTMu.Unlock()
	}()
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		var reply chan AuthChatGPTLoginResult
		select {
		case <-ctx.Done():
			_ = binding.manager.Cancel(id)
		case reply = <-binding.polls:
		case <-ticker.C:
		}
		attempt, err := binding.manager.Poll(id)
		if err != nil {
			attempt = siwc.Attempt{ID: id, Status: "failed", ErrorMessage: "ChatGPT login was not found; start again"}
		}
		result := s.chatGPTAttemptResult(ctx, binding.manager, attempt, false)
		if attempt.Status != "pending" {
			binding.result = result
		}
		if reply != nil {
			reply <- result
		}
		if attempt.Status != "pending" {
			return
		}
	}
}

func (s *Server) closeChatGPTLogins() {
	s.closeXAILogins()
	s.chatGPTMu.Lock()
	bindings := s.chatGPTBindings
	s.chatGPTBindings = nil
	s.chatGPTMu.Unlock()
	for _, binding := range bindings {
		binding.cancel()
	}
	for _, binding := range bindings {
		<-binding.done
	}
}

func (s *Server) chatGPTLoginBinding(id string) (*chatGPTBinding, error) {
	s.chatGPTMu.Lock()
	defer s.chatGPTMu.Unlock()
	binding := s.chatGPTBindings[id]
	if binding == nil {
		return nil, errors.New("ChatGPT login was not found; start again")
	}
	return binding, nil
}

func (s *Server) handleAuthChatGPT(ctx context.Context, req Request) error {
	var params AuthChatGPTLoginParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.closed.Load() {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	if req.Method == "auth/chatgpt/login/poll" || req.Method == "auth/chatgpt/login/cancel" {
		binding, err := s.chatGPTLoginBinding(params.LoginID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if req.Method == "auth/chatgpt/login/cancel" {
			binding.cancel()
			select {
			case <-binding.done:
				return s.writeResponse(req.ID, map[string]bool{"ok": true}, nil)
			case <-ctx.Done():
				return s.writeResponse(req.ID, nil, ctx.Err())
			}
		}
		result, err := binding.poll(ctx)
		return s.writeResponse(req.ID, result, err)
	}
	services, release, err := s.modelServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	manager := services.ChatGPT
	if manager == nil {
		return s.writeResponse(req.ID, nil, errors.New("selected ChatGPT credential manager is unavailable"))
	}
	switch req.Method {
	case "auth/chatgpt/login/start":
		attempt, err := manager.Start(ctx, siwc.StartOptions{AccountID: params.AccountID, ForceConsent: params.ForceConsent})
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if _, err := s.retainChatGPTLogin(services, release, attempt); err != nil {
			_ = manager.Cancel(attempt.ID)
			return s.writeResponse(req.ID, nil, err)
		}
		release = nil
		return s.writeResponse(req.ID, s.chatGPTAttemptResult(ctx, manager, attempt, true), nil)
	case "auth/chatgpt/accounts":
		accounts, err := manager.ListAccounts(ctx)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		out := make([]ChatGPTAccountSummary, 0, len(accounts))
		for _, a := range accounts {
			out = append(out, ChatGPTAccountSummary{ID: a.ID, Email: a.Email, Status: chatGPTAccountStatus(a)})
		}
		return s.writeResponse(req.ID, map[string]any{"accounts": out}, nil)
	case "auth/chatgpt/signout":
		result, err := manager.SignOut(ctx, params.AccountID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		s.chatGPTMu.Lock()
		delete(s.chatGPTModelCache, params.AccountID)
		s.chatGPTMu.Unlock()
		return s.writeResponse(req.ID, map[string]any{"ok": true, "revocation_confirmed": result.RevocationConfirmed, "providers": s.providerSummaries()}, nil)
	case "auth/chatgpt/models":
		models, err := s.discoverChatGPTModelsWithManager(ctx, manager, params.AccountID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if err := s.saveChatGPTCatalogWithSource(ctx, services.Config, params.AccountID, models); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		type choice struct {
			ProviderModelSummary
			Name string `json:"name"`
		}
		out := make([]choice, 0, len(models))
		for _, m := range models {
			out = append(out, choice{ProviderModelSummary: chatGPTModelSummary("chatgpt", config.ProviderConfig{Type: "chatgpt"}, config.ChatGPTModelConfig{ID: m.Slug, DisplayName: m.DisplayName, AccountDescriptor: m.Raw}), Name: m.DisplayName})
		}
		return s.writeResponse(req.ID, map[string]any{"models": out, "providers": s.providerSummaries()}, nil)
	}
	return s.writeResponse(req.ID, nil, errors.New("unsupported ChatGPT account operation"))
}
func (s *Server) chatGPTAttemptResult(ctx context.Context, manager *siwc.Manager, attempt siwc.Attempt, includeURL bool) AuthChatGPTLoginResult {
	status := attempt.Status
	if status == "succeeded" {
		status = "success"
	}
	remaining := int(time.Until(attempt.ExpiresAt).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	result := AuthChatGPTLoginResult{LoginID: attempt.ID, Status: status, AccountID: attempt.AccountID, IntervalMS: 750, ExpiresIn: remaining, Error: attempt.ErrorMessage}
	if includeURL {
		result.AuthorizationURL = attempt.AuthorizationURL
	}
	if status == "success" {
		// A completed reauthorization replaces the account's entitlement catalog.
		// Login remains successful if discovery fails; expose no stale model choices.
		s.chatGPTMu.Lock()
		delete(s.chatGPTModelCache, attempt.AccountID)
		s.chatGPTMu.Unlock()
		if err := s.saveChatGPTCatalog(ctx, manager, attempt.AccountID, nil); err != nil {
			result.Error = "Signed in, but saved model choices could not be refreshed. Retry in Model connections."
		} else {
			discoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			models, err := s.discoverChatGPTModelsWithManager(discoveryCtx, manager, attempt.AccountID)
			cancel()
			if err == nil {
				err = s.saveChatGPTCatalog(ctx, manager, attempt.AccountID, models)
			}
			if err != nil {
				result.Error = "Signed in, but available models could not be loaded. Retry in Model connections."
			}
		}
		result.Providers = s.providerSummaries()
	}
	return result
}
func chatGPTAccountStatus(account siwc.Account) string {
	if !account.Connected {
		return "reauth_required"
	}
	if !account.PlanEnabled {
		return "permission_required"
	}
	return "connected"
}
func (s *Server) discoverChatGPTModelsWithManager(ctx context.Context, manager *siwc.Manager, accountID string) ([]chatgpt.ModelInfo, error) {
	client, err := chatgpt.New(chatgpt.ClientConfig{AccountID: accountID, AccessToken: manager.AccessToken, BindRequest: manager.BindRequest, HTTPClient: s.chatGPTHTTP})
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	s.chatGPTMu.Lock()
	defer s.chatGPTMu.Unlock()
	if s.chatGPTModelCache == nil {
		s.chatGPTModelCache = map[string][]chatgpt.ModelInfo{}
	}
	s.chatGPTModelCache[accountID] = models
	return models, nil
}
func (s *Server) validateChatGPTModel(ctx context.Context, provider config.ProviderConfig, manager *siwc.Manager) error {
	if manager == nil {
		return errors.New("selected ChatGPT credential manager is unavailable")
	}
	if strings.TrimSpace(provider.ChatGPTAccountID) == "" {
		return errors.New("choose a signed-in ChatGPT account")
	}
	models, err := s.discoverChatGPTModelsWithManager(ctx, manager, provider.ChatGPTAccountID)
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.Slug == provider.Model {
			return nil
		}
	}
	return errors.New("choose a model available to this ChatGPT account")
}
func (s *Server) enrichChatGPTSummaries(summaries []ProviderSummary, cfg config.Config, manager *siwc.Manager) {
	for i := range summaries {
		summary := &summaries[i]
		if summary.Type != "chatgpt" {
			continue
		}
		summary.AuthStatus = "reauth_required"
		summary.APIKeyConfigured = false
		if manager == nil {
			continue
		}
		account, err := manager.Account(context.Background(), summary.ChatGPTAccountID)
		if err == nil {
			summary.AuthStatus = chatGPTAccountStatus(account)
			summary.APIKeyConfigured = summary.AuthStatus == "connected"
		}
		if summary.AuthStatus != "connected" {
			summary.Models = nil
			continue
		}
		s.chatGPTMu.Lock()
		models := append([]chatgpt.ModelInfo(nil), s.chatGPTModelCache[summary.ChatGPTAccountID]...)
		s.chatGPTMu.Unlock()
		if len(models) > 0 {
			summary.Models = make([]ProviderModelSummary, 0, len(models))
			for _, model := range models {
				summary.Models = append(summary.Models, chatGPTModelSummary(summary.Name, cfg.Providers[summary.Name], config.ChatGPTModelConfig{ID: model.Slug, DisplayName: model.DisplayName, AccountDescriptor: model.Raw}))
			}
		}
	}
}

// Keep composer choices available after restart without deriving entitlements
// from the API-key catalog. This write never moves a saved model or default.
func (s *Server) saveChatGPTCatalog(ctx context.Context, manager *siwc.Manager, accountID string, models []chatgpt.ModelInfo) error {
	var selected *config.Client
	s.chatGPTMu.Lock()
	for _, binding := range s.chatGPTBindings {
		if binding.manager == manager && binding.services != nil {
			selected = binding.services.Config
			break
		}
	}
	s.chatGPTMu.Unlock()
	if selected == nil {
		return errors.New("ChatGPT login configuration owner is unavailable")
	}
	return s.saveChatGPTCatalogWithSource(ctx, selected, accountID, models)
}

func (s *Server) saveChatGPTCatalogWithSource(ctx context.Context, selected *config.Client, accountID string, models []chatgpt.ModelInfo) error {
	s.configRefreshMu.Lock()
	defer s.configRefreshMu.Unlock()
	catalog := make([]config.ChatGPTModelConfig, 0, len(models))
	for _, model := range models {
		catalog = append(catalog, config.ChatGPTModelConfig{ID: model.Slug, DisplayName: model.DisplayName, AccountDescriptor: model.Raw})
	}
	_, err := updateConfigSourceClient(ctx, selected, func(source []byte) ([]byte, error) {
		var document struct {
			Providers map[string]config.ProviderConfig `json:"providers"`
		}
		if err := json.Unmarshal(source, &document); err != nil {
			return nil, errors.New("selected configuration cannot be edited")
		}
		next := source
		for name, provider := range document.Providers {
			if provider.Type == "chatgpt" && provider.ChatGPTAccountID == accountID {
				var err error
				next, err = config.UpdateProviderSource(next, name, config.ProviderChange{Model: provider.Model, ChatGPTModels: &catalog})
				if err != nil {
					return nil, err
				}
			}
		}
		return next, nil
	})
	return err
}
