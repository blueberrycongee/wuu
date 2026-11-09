package pluginsdk

import (
	"io/fs"
	"path"
	"regexp"
	"testing"
)

// The single-file development loader has no checkout to resolve missing public
// SDK modules. A newly exported module must ship in the embedded import graph.
func TestEmbeddedSDKRelativeImportsAreMaterializable(t *testing.T) {
	imports := regexp.MustCompile(`(?:from\s*|import\s*\()\s*["'](\./[^"']+)\.js["']`)
	err := fs.WalkDir(Sources, "src", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		source, err := Sources.ReadFile(name)
		if err != nil {
			return err
		}
		for _, match := range imports.FindAllSubmatch(source, -1) {
			target := path.Join(path.Dir(name), string(match[1])+".ts")
			if _, err := Sources.ReadFile(target); err != nil {
				t.Errorf("embedded SDK %s imports unavailable %s: %v", name, target, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
