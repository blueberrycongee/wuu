package acceptance_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAcceptanceBrowserStateProviderParity(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl", "external"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			host, documents, _, unused := startAcceptanceProvider(t, kind, dir)
			legacy := filepath.Join(t.TempDir(), "browser-tabs.json")
			require.NoError(t, tools.NewBrowserTabStore(legacy).Put(tools.BrowserTabRecord{TabID: "tab", URL: "https://example.org", Status: "retained"}))
			store := tools.NewBrowserTabDocumentStore(documents, "thread", legacy)
			record, found, err := store.Get("tab")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "retained", record.Status)
			require.NoError(t, store.Put(tools.BrowserTabRecord{TabID: "tab", URL: "https://example.org/updated", Dead: true}))
			outcome, found := host.RetirePlugin(context.Background(), "acceptance.selected", errors.New("disabled"))
			require.True(t, found)
			require.NoError(t, outcome.Err)
			require.Equal(t, pluginhost.StateActive, unused.Status().State)
			_, err = store.List()
			require.Error(t, err)
			require.NoError(t, host.Close(context.Background()))
			_, reopened, _, _ := startAcceptanceProvider(t, kind, dir)
			recovered := tools.NewBrowserTabDocumentStore(reopened, "thread", legacy)
			record, found, err = recovered.Get("tab")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "https://example.org/updated", record.URL)
			require.True(t, record.Dead)
			records, err := tools.NewBrowserTabDocumentStore(reopened, "other").List()
			require.NoError(t, err)
			require.Empty(t, records)
			require.NoError(t, recovered.Delete("tab"))
			records, err = tools.NewBrowserTabDocumentStore(reopened, "thread", legacy).List()
			require.NoError(t, err)
			require.Empty(t, records)
		})
	}
}
