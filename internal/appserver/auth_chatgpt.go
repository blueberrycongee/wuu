package appserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/chatgpt"
	"github.com/blueberrycongee/wuu/internal/siwc"
)

type chatGPTBinding struct {
	manager *siwc.Manager
	release func()
}

func (s *Server) chatGPTAuth() (*siwc.Manager, error) {
	s.chatGPTMu.Lock()
	defer s.chatGPTMu.Unlock()
	// An explicitly supplied test/protocol manager has its own owner.
	if s.chatGPTManager != nil {
		return s.chatGPTManager, nil
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return nil, err
	}
	if binding := s.chatGPTBindings[services.GenerationID]; binding != nil {
		release()
		return binding.manager, nil
	}
	manager := services.ChatGPT
	if manager == nil {
		release()
		return nil, errors.New("selected ChatGPT credential manager is unavailable")
	}
	if s.chatGPTBindings == nil {
		s.chatGPTBindings = make(map[string]*chatGPTBinding)
	}
	s.chatGPTBindings[services.GenerationID] = &chatGPTBinding{manager: manager, release: release}
	return manager, nil
}

func (s *Server) buildConfiguredStreamClient(provider config.ProviderConfig, name string) (providers.StreamClient, error) {
	if !strings.EqualFold(strings.TrimSpace(provider.Type), "chatgpt") {
		return providerfactory.BuildStreamClient(provider, name)
	}
	manager, err := s.chatGPTAuth()
	if err != nil {
		return nil, err
	}
	return providerfactory.BuildStreamClient(provider, name, manager)
}

func (s *Server) chatGPTClient(accountID string) (*chatgpt.Client, error) {
	manager, err := s.chatGPTAuth()
	if err != nil {
		return nil, err
	}
	return chatgpt.New(chatgpt.ClientConfig{AccountID: accountID, AccessToken: manager.AccessToken, BindRequest: manager.BindRequest, HTTPClient: s.chatGPTHTTP})
}
func (s *Server) closeChatGPTLogins() {
	s.chatGPTMu.Lock()
	injected, attempts, bindings := s.chatGPTManager, s.chatGPTAttempts, s.chatGPTBindings
	s.chatGPTBindings = nil
	s.chatGPTAttempts = nil
	s.chatGPTMu.Unlock()
	if injected != nil {
		for id := range attempts {
			_ = injected.Cancel(id)
		}
	}
	for _, binding := range bindings {
		for id := range attempts {
			_ = binding.manager.Cancel(id)
		}
		binding.release()
	}
}

// Pending login IDs continue on the manager that created them even after the
// selected credential provider changes for newly admitted operations.
func (s *Server) chatGPTLoginManager(id string) (*siwc.Manager, error) {
	s.chatGPTMu.Lock()
	defer s.chatGPTMu.Unlock()
	if !s.chatGPTAttempts[id] {
		return nil, errors.New("ChatGPT login was not found; start again")
	}
	if s.chatGPTManager != nil {
		return s.chatGPTManager, nil
	}
	for _, binding := range s.chatGPTBindings {
		if _, err := binding.manager.Poll(id); err == nil {
			return binding.manager, nil
		}
	}
	return nil, errors.New("ChatGPT login owner is unavailable")
}

func (s *Server) handleAuthChatGPT(ctx context.Context, req Request) error {
	var params AuthChatGPTLoginParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.closed.Load() {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	var manager *siwc.Manager
	var err error
	if req.Method == "auth/chatgpt/login/poll" || req.Method == "auth/chatgpt/login/cancel" {
		manager, err = s.chatGPTLoginManager(params.LoginID)
	} else {
		manager, err = s.chatGPTAuth()
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	switch req.Method {
	case "auth/chatgpt/login/start":
		attempt, err := manager.Start(ctx, siwc.StartOptions{AccountID: params.AccountID, ForceConsent: params.ForceConsent})
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		s.chatGPTMu.Lock()
		if s.chatGPTAttempts == nil {
			s.chatGPTAttempts = map[string]bool{}
		}
		s.chatGPTAttempts[attempt.ID] = true
		s.chatGPTMu.Unlock()
		if s.closed.Load() {
			_ = manager.Cancel(attempt.ID)
			return s.writeResponse(req.ID, nil, errServerClosed)
		}
		return s.writeResponse(req.ID, s.chatGPTAttemptResult(manager, attempt, true), nil)
	case "auth/chatgpt/login/poll", "auth/chatgpt/login/cancel":
		s.chatGPTMu.Lock()
		owned := s.chatGPTAttempts[params.LoginID]
		s.chatGPTMu.Unlock()
		if !owned {
			return s.writeResponse(req.ID, nil, errors.New("ChatGPT login was not found; start again"))
		}
		if req.Method == "auth/chatgpt/login/cancel" {
			err := manager.Cancel(params.LoginID)
			return s.writeResponse(req.ID, map[string]bool{"ok": err == nil}, err)
		}
		attempt, err := manager.Poll(params.LoginID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		return s.writeResponse(req.ID, s.chatGPTAttemptResult(manager, attempt, false), nil)
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
		models, err := s.discoverChatGPTModels(ctx, params.AccountID)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if err := s.saveChatGPTCatalog(params.AccountID, models); err != nil {
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
func (s *Server) chatGPTAttemptResult(manager *siwc.Manager, attempt siwc.Attempt, includeURL bool) AuthChatGPTLoginResult {
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
		if err := s.saveChatGPTCatalog(attempt.AccountID, nil); err != nil {
			result.Error = "Signed in, but saved model choices could not be refreshed. Retry in Model connections."
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			models, err := s.discoverChatGPTModelsWithManager(ctx, manager, attempt.AccountID)
			cancel()
			if err == nil {
				err = s.saveChatGPTCatalog(attempt.AccountID, models)
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
func (s *Server) discoverChatGPTModels(ctx context.Context, accountID string) ([]chatgpt.ModelInfo, error) {
	manager, err := s.chatGPTAuth()
	if err != nil {
		return nil, err
	}
	return s.discoverChatGPTModelsWithManager(ctx, manager, accountID)
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
func (s *Server) validateChatGPTModel(ctx context.Context, provider config.ProviderConfig) error {
	if strings.TrimSpace(provider.ChatGPTAccountID) == "" {
		return errors.New("choose a signed-in ChatGPT account")
	}
	models, err := s.discoverChatGPTModels(ctx, provider.ChatGPTAccountID)
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
func (s *Server) enrichChatGPTSummaries(summaries []ProviderSummary, cfg config.Config) {
	for i := range summaries {
		summary := &summaries[i]
		if summary.Type != "chatgpt" {
			continue
		}
		summary.AuthStatus = "reauth_required"
		summary.APIKeyConfigured = false
		manager, err := s.chatGPTAuth()
		if err != nil {
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
func (s *Server) saveChatGPTCatalog(accountID string, models []chatgpt.ModelInfo) error {
	s.configRefreshMu.Lock()
	defer s.configRefreshMu.Unlock()
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return err
	}
	catalog := make([]config.ChatGPTModelConfig, 0, len(models))
	for _, model := range models {
		catalog = append(catalog, config.ChatGPTModelConfig{ID: model.Slug, DisplayName: model.DisplayName, AccountDescriptor: model.Raw})
	}
	for name, provider := range cfg.Providers {
		if provider.Type == "chatgpt" && provider.ChatGPTAccountID == accountID {
			if err := config.UpdateProvider(s.rt.ConfigPath, name, config.ProviderChange{Model: provider.Model, ChatGPTModels: &catalog}); err != nil {
				return err
			}
		}
	}
	return nil
}
