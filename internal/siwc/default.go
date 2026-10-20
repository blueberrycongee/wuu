package siwc

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

var defaults struct {
	sync.Mutex
	managers map[string]*Manager
}

// DefaultManager shares account refresh and registration ownership within a
// process. Desktop credentials stay in the system keyring; failure is visible.
func DefaultManager() (*Manager, error) {
	home, err := statepath.Home(os.Getenv("HOME"))
	if err != nil {
		return nil, err
	}
	defaults.Lock()
	defer defaults.Unlock()
	if manager := defaults.managers[home]; manager != nil {
		return manager, nil
	}
	credentials, err := credentialstore.NewDesktopStore()
	if err != nil {
		return nil, err
	}
	manager, err := New(Config{StateDir: filepath.Join(home, "chatgpt"), Credentials: credentials})
	if err != nil {
		return nil, err
	}
	if defaults.managers == nil {
		defaults.managers = map[string]*Manager{}
	}
	defaults.managers[home] = manager
	return manager, nil
}
