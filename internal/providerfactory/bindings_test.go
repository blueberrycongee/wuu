package providerfactory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestFactorySelectedCredentialsAndExplicitOverrides(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", filepath.Join(home, ".wuu"))
	t.Setenv("OPENAI_API_KEY", "ambient")
	legacy, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Set("gateway", authstorage.Credentials{APIKey: "unselected-native", Source: authstorage.SourceSaved}); err != nil {
		t.Fatal(err)
	}
	selected := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err := authstorage.NewBound(ctx, selected).Set("gateway", authstorage.Credentials{APIKey: "selected-secret", Source: authstorage.SourceSaved}); err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer selected-secret" {
			t.Error("factory used an unselected credential source")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer endpoint.Close()
	provider := config.ProviderConfig{Type: "openai-compatible", BaseURL: endpoint.URL, Model: "fixture"}
	client, err := BuildClient(provider, "gateway", Bindings{Credentials: selected})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(ctx, providers.ChatRequest{Model: "fixture", Messages: []providers.ChatMessage{{Role: "user", Content: "fixture"}}}); err != nil {
		t.Fatal(err)
	}
	unavailable := credentialstore.NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("private-transport-detail")
	})
	if _, err := BuildClient(provider, "gateway", Bindings{Credentials: unavailable}); err == nil || strings.Contains(err.Error(), "private-transport-detail") {
		t.Fatal("selected failure fell back to ambient/native credentials or leaked details")
	}
	provider.APIKey = "explicit"
	if _, err := BuildClient(provider, "gateway", Bindings{Credentials: unavailable}); err != nil {
		t.Fatalf("explicit config credential lost precedence: %v", err)
	}
	provider.APIKey = ""
	provider.APIKeyEnv = "EXPLICIT_FACTORY_KEY"
	t.Setenv("EXPLICIT_FACTORY_KEY", "explicit-env")
	if _, err := BuildClient(provider, "gateway", Bindings{Credentials: unavailable}); err != nil {
		t.Fatalf("explicit environment credential lost precedence: %v", err)
	}
}

func testNativeBindings(t *testing.T, home string) Bindings {
	t.Helper()
	resolved, err := statepath.Home(home)
	if err != nil {
		t.Fatal(err)
	}
	store, err := authstorage.NewNativeStore(credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json")), resolved, home)
	if err != nil {
		t.Fatal(err)
	}
	return Bindings{Credentials: store}
}
