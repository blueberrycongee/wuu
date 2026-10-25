package evalharness

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
)

// The catalog contains task setup, prompts, constraints and independent graders.
// Embedding its source binds a frozen plan to the code in the executable, not
// to whichever checkout happens to be current when an experiment is rerun.
//
//go:embed tasks.go
var catalogSource []byte

func CatalogVersion() string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(catalogSource))
}
