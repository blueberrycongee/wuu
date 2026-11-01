// Package pluginprogram adapts local source files to the public process protocol.
package pluginprogram

import (
	"context"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

func SingleFileID(source string) string {
	return pluginpkg.SingleFileID(source)
}

// PrepareSingleFile adapts a single-file TypeScript plugin (RuntimePlugin,
// Desktop UI plugin, or unified full-stack plugin) to a synthetic plugin package.
func PrepareSingleFile(ctx context.Context, source string) (string, func(), error) {
	return pluginpkg.PrepareSingleFile(ctx, source)
}

