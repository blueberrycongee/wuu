package sdk

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// The same SDK runtime can serve its private desktop pipe or authenticated
// peers. A supplied identity, even LocalOwner, never opts a peer into forwarding.
func TestSDKTrustedTransportRequiresLocalIngress(t *testing.T) {
	cfg := config.Default()
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "minimal"}
	plugins := []pluginpkg.Plugin{}
	bindings := appserver.NewRuntimeBindings()
	core, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NativeProviders: bindings.NativeProviders(), NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{core: core, bindings: bindings}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	}()
	guest, err := principal.Authenticated(principal.Identity{Kind: "agent", Issuer: "fixture", Tenant: "tenant", Subject: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		device bool
		want   string
	}{
		{name: "private desktop pipe", ctx: context.Background(), want: "transport receipt unavailable"},
		{name: "authenticated peer", ctx: principal.WithContext(context.Background(), guest), want: "trusted local transport required"},
		{name: "authenticated owner is not ingress", ctx: principal.WithContext(context.Background(), principal.LocalOwner()), want: "trusted local transport required"},
		{name: "device owner", ctx: principal.WithContext(context.Background(), principal.LocalOwner()), device: true, want: "trusted local transport required"},
		{name: "device without identity", ctx: context.Background(), device: true, want: "authenticated device principal is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			input := strings.NewReader(`{"id":"spoof","method":"transport/check","params":{"version":1,"peer_id":"forged","owner":true,"receipt":"invented"}}` + "\n")
			var err error
			if tc.device {
				err = rt.ServeForDevice(tc.ctx, input, &out, nil)
			} else {
				err = rt.Serve(tc.ctx, input, &out)
			}
			actual := out.String()
			if err != nil {
				actual += " " + err.Error()
			}
			if !strings.Contains(actual, tc.want) {
				t.Fatalf("got %s; want %s", actual, tc.want)
			}
		})
	}
}
