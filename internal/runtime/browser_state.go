package runtime

import (
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func selectedBrowserTabStore(documents persistence.Documents, home, sessionsDir, stateDir, id string) (tools.BrowserTabStore, error) {
	roots, err := statepath.SessionArtifactStateDirs(home, sessionsDir, stateDir, id)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(roots))
	for _, root := range roots {
		paths = append(paths, statepath.ThreadBrowserTabsPath(root, id))
	}
	return tools.NewBrowserTabDocumentStore(documents, id, paths...), nil
}
