package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestLocalSkillRefreshKeepsSessionRootAlias(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "workspace")
	alias := filepath.Join(base, "workspace-alias")
	require.NoError(t, os.Mkdir(real, 0700))
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeSkill := func(name string) {
		dir := filepath.Join(real, ".agents", "skills", name)
		require.NoError(t, os.MkdirAll(dir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Example skill.\n---\nInstructions.\n"), 0600))
	}
	writeSkill("original")
	s := &Session{RootDir: alias, HomeDir: t.TempDir(), WuuHome: t.TempDir()}
	kit, err := tools.New(real)
	require.NoError(t, err)
	kit.SetSkills(discoverSkillsWithPlugins(alias, s.HomeDir, s.WuuHome, pluginSkillSnapshot{}, config.SkillsConfig{}))
	s.bindLocalSkillRefresh(kit, real, nil)
	notices, err := kit.PrepareModelStep(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, notices, "the same directory alias must not change the initial catalog")

	writeSkill("new-skill")
	notices, err = kit.PrepareModelStep(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0].Content, "new-skill", "actual filesystem changes remain visible")

	other := filepath.Join(base, "other-workspace")
	otherSkill := filepath.Join(other, ".agents", "skills", "other-skill")
	require.NoError(t, os.MkdirAll(otherSkill, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(otherSkill, "SKILL.md"), []byte("---\nname: other-skill\ndescription: Other workspace skill.\n---\nInstructions.\n"), 0600))
	otherKit, err := tools.New(other)
	require.NoError(t, err)
	otherKit.SetSkills(discoverSkillsWithPlugins(other, s.HomeDir, s.WuuHome, pluginSkillSnapshot{}, config.SkillsConfig{}))
	s.bindLocalSkillRefresh(otherKit, other, nil)
	otherSkills, err := otherKit.CurrentSkills(context.Background(), true)
	require.NoError(t, err)
	var names []string
	for _, skill := range otherSkills {
		names = append(names, skill.Name)
	}
	require.Contains(t, names, "other-skill")
	require.NotContains(t, names, "original", "different roots keep separate catalogs")
}
