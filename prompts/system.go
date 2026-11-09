package prompts

import (
	_ "embed"
	"strings"
)

//go:embed system.md
var system string

//go:embed system_main.md
var systemMain string

// System returns the base prompt shared by every agent session. Optional
// products contribute their own prompt sections through capabilities.
func System() string {
	return strings.TrimSpace(system)
}

// SystemMain returns universal main-session guidance. Optional product
// guidance must not be embedded here; it is supplied by the owning plugin.
func SystemMain() string {
	return strings.TrimSpace(systemMain)
}
