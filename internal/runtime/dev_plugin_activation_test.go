package runtime

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

func TestActivatedPluginsTrustsOnlyAuthenticatedDevProvenance(t *testing.T) {
	dev := pluginpkg.Plugin{AuthorizedDev: true}
	dev.ID = "dev-plugin"
	dev.SubjectID = "plugin:dev-plugin"
	dev.Fingerprint = "new-fingerprint"
	community := dev
	community.ID = "community-plugin"
	community.SubjectID = "plugin:community-plugin"
	community.AuthorizedDev = false

	active := activatedPlugins(config.Config{}, []pluginpkg.Plugin{dev, community})
	if len(active) != 1 || active[0].ID != dev.ID {
		t.Fatalf("active plugins = %+v", active)
	}
}
