package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/plugincredentials"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// The real plugin gateway must use its injected store, never open a system
// store when the selection is absent, and redact an echoed bearer token.
func TestPluginCredentialRequestUsesCapturedStoreAndStopsWithoutBinding(t *testing.T) {
	token := "fixture-only-bearer-value"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+token {
			t.Error("injected credential was not used")
		}
		fmt.Fprint(w, req.Header.Get("Authorization"))
	}))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	home := t.TempDir()
	item := pluginpkg.Plugin{ID: "credential-demo", SubjectID: "plugin:user:credential-demo", Fingerprint: "generation-one", Settings: map[string]pluginpkg.SettingDefinition{"credential-demo.token": {Type: pluginpkg.SettingTypeSecret, CredentialOrigins: []string{server.URL}}}}
	services := newPluginHostServices(item, t.TempDir(), home, nil)
	services.activate()
	defer services.close()
	store := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "selected.json"))
	binding, err := plugincredentials.BindingFor(home, services.projectRoot, item.SubjectID, "token", []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := plugincredentials.Set(context.Background(), store, binding, token); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(plugincredentials.RequestParams{Credential: "token", URL: server.URL, Method: "GET"})
	_, err = services.invoke(context.Background(), pluginhost.HostServiceCredentialsRequest, request)
	var serviceErr *pluginhost.HostServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != "service_unavailable" {
		t.Fatalf("missing binding did not fail closed: %v", err)
	}
	services.credentials = store
	response, err := services.invoke(context.Background(), pluginhost.HostServiceCredentialsRequest, request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response), token) || !strings.Contains(string(response), "[redacted]") {
		t.Fatal("credential response did not redact the selected token")
	}
	services.close()
	if _, err := services.invoke(context.Background(), pluginhost.HostServiceCredentialsRequest, request); err == nil {
		t.Fatal("closed credential generation remained callable")
	}
}
