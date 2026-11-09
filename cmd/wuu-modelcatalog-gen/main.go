package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/modelcatalog"
)

func main() {
	input := flag.String("input", modelcatalog.ModelsDevURL, "models.dev api.json URL or local path")
	output := flag.String("output", "internal/modelcatalog/models_dev_catalog.json", "output catalog JSON path")
	legacy := flag.String("legacy-output", "", "optional retired-model archive to update")
	previous := flag.String("previous", "", "previous normalized catalog (defaults to output)")
	flag.Parse()

	data, err := readInput(*input)
	if err != nil {
		fatal(err)
	}
	encoded, _, err := modelcatalog.NormalizeModelsDev(data)
	if err != nil {
		fatal(fmt.Errorf("normalize %s: %w", *input, err))
	}
	if *legacy != "" {
		if *previous == "" {
			*previous = *output
		}
		if err := updateLegacy(*legacy, *previous, encoded); err != nil {
			fatal(err)
		}
	}
	if err := os.WriteFile(*output, encoded, 0o644); err != nil {
		fatal(err)
	}
}

// Keep removed metadata for saved selections, without advertising retired models.
func updateLegacy(path, previous string, current []byte) error {
	type catalog struct {
		Providers []modelcatalog.Provider `json:"providers"`
	}
	var active catalog
	if err := json.Unmarshal(current, &active); err != nil {
		return err
	}
	activeIDs := make(map[string]map[string]bool)
	for _, provider := range active.Providers {
		ids := make(map[string]bool)
		for _, model := range provider.Models {
			ids[model.ID] = true
		}
		activeIDs[provider.ID] = ids
	}
	providers := make(map[string]modelcatalog.Provider)
	models := make(map[string]map[string]modelcatalog.Model)
	for _, source := range []string{path, previous} {
		raw, err := os.ReadFile(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var old catalog
		if err := json.Unmarshal(raw, &old); err != nil {
			return fmt.Errorf("decode %s: %w", source, err)
		}
		for _, provider := range old.Providers {
			providers[provider.ID] = provider
			if models[provider.ID] == nil {
				models[provider.ID] = make(map[string]modelcatalog.Model)
			}
			for _, model := range provider.Models {
				if !activeIDs[provider.ID][model.ID] {
					models[provider.ID][model.ID] = model
				}
			}
		}
	}
	retired := catalog{Providers: []modelcatalog.Provider{}}
	for id, entries := range models {
		if len(entries) == 0 {
			continue
		}
		provider := providers[id]
		provider.Models = nil
		for _, model := range entries {
			provider.Models = append(provider.Models, model)
		}
		sort.Slice(provider.Models, func(i, j int) bool { return provider.Models[i].ID < provider.Models[j].ID })
		retired.Providers = append(retired.Providers, provider)
	}
	sort.Slice(retired.Providers, func(i, j int) bool { return retired.Providers[i].ID < retired.Providers[j].ID })
	encoded, err := json.Marshal(retired)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func readInput(input string) ([]byte, error) {
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		client := http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(input)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch %s: %s", input, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(input)
}
