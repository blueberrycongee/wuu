package evalharness

import (
	"crypto/sha256"
	"embed"
	"fmt"
)

// The catalog contains task setup, prompts, constraints and independent graders.
// Embedding its source binds a frozen plan to the code in the executable, not
// to whichever checkout happens to be current when an experiment is rerun.
//
//go:embed tasks.go trace.go
var catalogSource embed.FS

func CatalogVersion() string {
	hash := sha256.New()
	for _, name := range []string{"tasks.go", "trace.go"} {
		source, _ := catalogSource.ReadFile(name)
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(source))
		hash.Write(source)
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}
