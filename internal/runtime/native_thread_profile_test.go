package runtime

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/stretchr/testify/require"
)

func TestThreadNativeToolkitUsesResolvedProviderAndAPIModel(t *testing.T) {
	root := t.TempDir()
	owner := &Session{RootDir: root, WuuHome: t.TempDir(), ProviderName: "custom-alias", Model: "friendly-label", ModelRoles: modelroles.Set{Main: modelroles.Selection{RuleProvider: "openai", APIModel: "gpt-5-codex"}}}
	generation := &PluginGeneration{}
	kit, cleanup, err := owner.newThreadNativeToolkit(generation, root)
	require.NoError(t, err)
	defer func() { _ = cleanup(context.Background()); _, _ = generation.nativeDependencies.close() }()
	require.Equal(t, string(modelprofile.ProfileOpenAICodex), kit.ActiveSurface().ProfileName)
}
