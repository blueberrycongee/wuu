package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	textPolishTimeout   = 45 * time.Second
	textPolishMaxLength = 20_000
)

func (s *Server) handleTextPolish(req Request) error {
	var params TextPolishParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	text := strings.TrimSpace(params.Text)
	if text == "" {
		return s.writeResponse(req.ID, nil, errors.New("text is required"))
	}
	if len([]rune(text)) > textPolishMaxLength {
		return s.writeResponse(req.ID, nil, fmt.Errorf("text exceeds %d characters", textPolishMaxLength))
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("BYOK model runtime is not available"))
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	models, err := binding.Models()
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("BYOK model runtime is not available: %w", err))
	}
	if models.Main == nil {
		return s.writeResponse(req.ID, nil, errors.New("BYOK model runtime is not available"))
	}
	selection := models.Roles.Main
	model := strings.TrimSpace(selection.APIModel)
	if model == "" {
		model = strings.TrimSpace(selection.Model)
	}
	if model == "" {
		return s.writeResponse(req.ID, nil, errors.New("no BYOK model is configured"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), textPolishTimeout)
	defer cancel()
	ctx = providers.WithInferenceJournal(ctx, binding.InferenceJournalRuntime.ForOwner("text-polish"))
	response, err := providers.ExecuteChat(
		ctx,
		models.Main,
		providers.ChatRequest{
			Provider: selection.Provider,
			Model:    model,
			Messages: []providers.ChatMessage{
				{
					Role:    "system",
					Content: "Lightly polish dictated text. Correct recognition mistakes, punctuation, and awkward wording while preserving the original meaning, language, technical terms, and level of detail. Return only the polished text. Do not answer questions or add commentary.",
				},
				{Role: "user", Content: text},
			},
			Temperature:     models.Temperature,
			Effort:          selection.LegacyEffort,
			ProviderOptions: provideroptions.Clone(selection.ProviderOptions),
		},
		providers.InferenceOperationAuxiliary,
		providers.InferenceProfileInteractive,
	)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("BYOK text polish failed: %w", err))
	}
	polished := strings.TrimSpace(response.Content)
	if polished == "" {
		return s.writeResponse(req.ID, nil, errors.New("BYOK model returned empty polished text"))
	}
	return s.writeResponse(req.ID, TextPolishResult{Text: polished}, nil)
}
