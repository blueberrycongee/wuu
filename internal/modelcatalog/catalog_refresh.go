package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxModelsDevResponseBytes = 16 << 20

type CatalogCounts struct {
	Providers int `json:"provider_count"`
	Models    int `json:"model_count"`
}

type RefreshOptions struct {
	URL       string
	CachePath string
	Client    *http.Client
}

var catalogStore = struct {
	sync.RWMutex
	initialized bool
	data        catalogData
	err         error
}{}

func catalogProviders() ([]Provider, error) {
	catalogStore.Lock()
	if !catalogStore.initialized {
		catalogStore.data, catalogStore.err = decodeCatalog(catalogJSON)
		catalogStore.initialized = true
	}
	data, err := catalogStore.data, catalogStore.err
	catalogStore.Unlock()
	if err != nil {
		return nil, err
	}
	return data.Providers, nil
}

// Keep saved MiniMax selections resolvable after upstream removes retired entries.
// Read the raw embedded snapshot once, without applying discovery filters.
var embeddedMiniMaxModels = sync.OnceValues(func() (map[string][]Model, error) {
	var data catalogData
	if err := json.Unmarshal(catalogJSON, &data); err != nil {
		return nil, err
	}
	models := make(map[string][]Model)
	for _, provider := range data.Providers {
		if isMiniMaxProviderID(provider.ID) {
			for _, model := range provider.Models {
				if isLegacyMiniMaxModel(model.ID) {
					models[provider.ID] = append(models[provider.ID], model)
				}
			}
		}
	}
	return models, nil
})

func decodeCatalog(data []byte) (catalogData, error) {
	var decoded catalogData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return catalogData{}, err
	}
	legacy, err := embeddedMiniMaxModels()
	if err != nil {
		return catalogData{}, err
	}
	for i := range decoded.Providers {
		provider := &decoded.Providers[i]
		for _, model := range legacy[normalizeID(provider.ID)] {
			if _, exists := modelByID(*provider, model.ID); !exists {
				provider.legacyModels = append(provider.legacyModels, model)
			}
		}
	}
	applyOfficialCatalogCorrections(&decoded)
	counts := countCatalog(decoded)
	if counts.Providers == 0 || counts.Models == 0 {
		return catalogData{}, errors.New("model catalog is empty")
	}
	return decoded, nil
}

func countCatalog(data catalogData) CatalogCounts {
	counts := CatalogCounts{Providers: len(data.Providers)}
	for _, provider := range data.Providers {
		counts.Models += len(provider.Models)
	}
	return counts
}

func publishCatalog(data catalogData) {
	catalogStore.Lock()
	catalogStore.initialized = true
	catalogStore.data = data
	catalogStore.err = nil
	catalogStore.Unlock()
}

// LoadCache publishes a previously normalized catalog cache. A missing cache
// is not an error; malformed data never replaces the current snapshot.
func LoadCache(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	decoded, err := decodeCatalog(data)
	if err != nil {
		return fmt.Errorf("decode cached model catalog: %w", err)
	}
	publishCatalog(decoded)
	return nil
}

// UseEmbedded restores the catalog shipped with this build. It is primarily
// useful to isolate tests after exercising refresh behavior.
func UseEmbedded() error {
	decoded, err := decodeCatalog(catalogJSON)
	if err != nil {
		return err
	}
	publishCatalog(decoded)
	return nil
}

// Refresh downloads, normalizes, and atomically persists a fresh catalog,
// then publishes it for immediate use. Failures leave both the prior cache and
// the prior in-memory snapshot unchanged.
func Refresh(ctx context.Context, options RefreshOptions) (CatalogCounts, error) {
	cachePath := strings.TrimSpace(options.CachePath)
	if cachePath == "" {
		return CatalogCounts{}, errors.New("model catalog cache path is required")
	}
	url := strings.TrimSpace(options.URL)
	if url == "" {
		url = ModelsDevURL
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else if client.Timeout <= 0 {
		clientCopy := *client
		clientCopy.Timeout = 30 * time.Second
		client = &clientCopy
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CatalogCounts{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return CatalogCounts{}, fmt.Errorf("fetch models.dev: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CatalogCounts{}, fmt.Errorf("fetch models.dev: %s", resp.Status)
	}
	limited := io.LimitReader(resp.Body, maxModelsDevResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return CatalogCounts{}, fmt.Errorf("read models.dev: %w", err)
	}
	if len(raw) > maxModelsDevResponseBytes {
		return CatalogCounts{}, errors.New("models.dev response exceeds size limit")
	}
	normalized, _, err := NormalizeModelsDev(raw)
	if err != nil {
		return CatalogCounts{}, err
	}
	decoded, err := decodeCatalog(normalized)
	if err != nil {
		return CatalogCounts{}, err
	}
	if err := writeCatalogAtomically(cachePath, normalized); err != nil {
		return CatalogCounts{}, err
	}
	publishCatalog(decoded)
	return countCatalog(decoded), nil
}

func writeCatalogAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create model catalog cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".modelcatalog-*.tmp")
	if err != nil {
		return fmt.Errorf("create model catalog cache: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write model catalog cache: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync model catalog cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close model catalog cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace model catalog cache: %w", err)
	}
	return nil
}
