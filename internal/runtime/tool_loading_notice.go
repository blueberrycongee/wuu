package runtime

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/config"
)

// unsupportedNativeWarned de-duplicates the fallback notice across the process
// lifetime. Tool loading is resolved on every session build and again on every
// provider or model switch, so an undeduped notice would repeat all run.
// The key includes provider and model so a switch to a different unsupported
// pair still reports once.
var unsupportedNativeWarned sync.Map

// unsupportedNativeWarnWriter is the sink for the notice. It defaults to
// stderr and is a test seam so the output can be asserted without pipe
// plumbing, matching the config package's mcp_json diagnostics.
var unsupportedNativeWarnWriter io.Writer = os.Stderr

func warnUnsupportedNativeToolLoadingOnce(providerCfg config.ProviderConfig, model string) {
	provider := strings.TrimSpace(providerCfg.BaseURL)
	if provider == "" {
		provider = strings.TrimSpace(providerCfg.Type)
	}
	if provider == "" {
		provider = "the configured provider"
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "the configured model"
	}
	key := provider + "\x00" + model
	if _, seen := unsupportedNativeWarned.LoadOrStore(key, struct{}{}); seen {
		return
	}
	fmt.Fprintf(unsupportedNativeWarnWriter,
		"wuu: agent.tool_loading = %q, but %s with model %q does not support provider-native deferred tool discovery. Falling back to %q, which loads schemas through ordinary tool discovery.\n",
		config.ToolLoadingNative, provider, model, config.ToolLoadingClient)
}

// resetUnsupportedNativeWarnings clears the process-level dedupe set.
// Test-only.
func resetUnsupportedNativeWarnings() {
	unsupportedNativeWarned.Range(func(k, _ any) bool {
		unsupportedNativeWarned.Delete(k)
		return true
	})
}
