package main

import (
	"context"
	"log"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
	generativeui "github.com/blueberrycongee/wuu/plugins/generative-ui"
)

func main() {
	if err := pluginapi.Serve(context.Background(), generativeui.Handler()); err != nil {
		log.Fatal(err)
	}
}
