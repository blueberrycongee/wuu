package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every embedded file under bundled/ (SKILL.md AND supporting references/,
// scripts/, assets/) must land on real disk, so a skill's siblings are
// reachable by path — not just its SKILL.md.
func TestMaterializeBundledWritesWholeTree(t *testing.T) {
	root, err := MaterializeBundled(t.TempDir())
	if err != nil {
		t.Fatalf("MaterializeBundled: %v", err)
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		t.Fatalf("materialized root not a real dir: %v", statErr)
	}

	var embedded []string
	_ = fs.WalkDir(bundledFS, "bundled", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		embedded = append(embedded, strings.TrimPrefix(strings.TrimPrefix(p, "bundled"), "/"))
		return nil
	})
	if len(embedded) == 0 {
		t.Fatal("no embedded bundled files found")
	}
	for _, rel := range embedded {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("embedded file %q not materialized: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, systemSkillsMarker)); err != nil {
		t.Fatalf("fingerprint marker missing: %v", err)
	}
}

// An unchanged fingerprint must skip the wipe-and-rewrite.
func TestMaterializeBundledIsIdempotent(t *testing.T) {
	cacheRoot := t.TempDir()
	first, err := MaterializeBundled(cacheRoot)
	if err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	sentinel := filepath.Join(first, "browser", "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	second, err := MaterializeBundled(cacheRoot)
	if err != nil {
		t.Fatalf("second materialize: %v", err)
	}
	if second != first {
		t.Fatalf("root changed: %q != %q", first, second)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("idempotent run rewrote unchanged tree (sentinel gone): %v", err)
	}
}

// The pptx-generator is a script-bearing skill: it must surface with a real Dir
// AND its references must be reachable on disk — the capability the whole
// materialization mechanism exists to enable.
func TestMergeWithBundledMaterializesPptxReferences(t *testing.T) {
	merged := MergeWithBundled(nil, t.TempDir())
	pptx, ok := Find(merged, "pptx-generator")
	if !ok {
		t.Fatalf("bundled 'pptx-generator' missing from %+v", merged)
	}
	if pptx.Description == "" {
		t.Fatal("pptx-generator description empty — would be hidden from catalog")
	}
	ref := filepath.Join(pptx.Dir, "references", "pptxgenjs.md")
	if _, err := os.Stat(ref); err != nil {
		t.Fatalf("skill reference %q not reachable on disk: %v", ref, err)
	}
}

// The skill-creator is the meta-skill that authors other skills: it must
// surface from the bundle with its frontmatter reference on disk, and it must
// pass the same lint it tells authors to run.
func TestMergeWithBundledSkillCreatorLintsClean(t *testing.T) {
	merged := MergeWithBundled(nil, t.TempDir())
	creator, ok := Find(merged, "skill-creator")
	if !ok {
		t.Fatalf("bundled 'skill-creator' missing from %+v", merged)
	}
	if creator.Description == "" {
		t.Fatal("skill-creator description empty — would be hidden from catalog")
	}
	ref := filepath.Join(creator.Dir, "references", "frontmatter.md")
	if _, err := os.Stat(ref); err != nil {
		t.Fatalf("skill reference %q not reachable on disk: %v", ref, err)
	}
	issues, err := LintPath(creator.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("skill-creator must lint clean, got %+v", issues)
	}
}
