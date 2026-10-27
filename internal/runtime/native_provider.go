package runtime

import (
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

// NativeProviderFactory assembles an embedding application's provider through
// the ordinary graph. Build runs for every prepared generation; its result must
// declare dependencies and defer effects until Activate, like a process peer.
type NativeProviderFactory struct {
	capacityOwner  *CapacityResources
	ID             string
	DefaultEnabled bool
	Build          func(NativeProviderContext) (pluginhost.Client, error)
}

type NativeProviderContext struct {
	DataRoot                      string
	UserBinding                   userspace.Binding
	RetainState                   func() (*State, error)
	Registry                      *pluginhost.ServiceRegistry
	Documents                     persistence.Documents
	Sessions                      *session.Store
	WuuHome, RootDir, WorkspaceID string
}
