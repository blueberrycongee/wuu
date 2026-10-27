package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/config"
)

// Retain one source for the complete edit. A conflict or unknown transport
// outcome is returned without replaying a possibly committed mutation.
func (s *Server) updateConfigSource(ctx context.Context, edit func([]byte) ([]byte, error)) (config.SourceView, error) {
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return config.SourceView{}, err
	}
	defer release()
	return updateConfigSourceClient(ctx, services.Config, edit)
}

func updateConfigSourceClient(ctx context.Context, sourceClient *config.Client, edit func([]byte) ([]byte, error)) (config.SourceView, error) {
	view, err := sourceClient.Read(ctx)
	if err != nil {
		return view, err
	}
	source, err := editableConfigSource(view)
	if err != nil {
		return config.SourceView{}, err
	}
	next, err := edit(source)
	if err != nil {
		return config.SourceView{}, err
	}
	if bytes.Equal(next, source) {
		return view, nil
	}
	view, err = sourceClient.CompareExchange(ctx, view.Revision, next)
	if err != nil {
		return view, err
	}
	return view, configActivationError(view)
}

func configActivationError(view config.SourceView) error {
	if view.ActivationError != "" {
		return errors.New("configuration was saved, but the selected provider could not activate it")
	}
	return nil
}

func editableConfigSource(view config.SourceView) ([]byte, error) {
	if len(view.Source) != 0 {
		return view.Source, nil
	}
	source, err := json.Marshal(view.Config)
	if err != nil {
		return nil, errors.New("selected configuration cannot be edited")
	}
	return source, nil
}
