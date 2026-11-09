package main

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginprogram"
)

func singleFilePluginID(source string) string { return pluginprogram.SingleFileID(source) }

func prepareSingleFilePlugin(ctx context.Context, source string) (string, func(), error) {
	return pluginprogram.PrepareSingleFile(ctx, source)
}
