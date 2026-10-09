package tools

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/skills"
)

func TestSkillAvailabilityRevokesFutureReadsAcrossClones(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	available := func(skill skills.Skill) bool { return skill.Source != "plugin:consumer" || !revoked.Load() }
	original := []skills.Skill{{Name: "dependent", Source: "plugin:consumer", Content: "old instructions"}, {Name: "independent", Source: "project", Content: "keep"}}
	kit.SetSkillsWithAvailability(original, available)
	clone, err := kit.CloneForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Worker rediscovery must preserve the inherited generation's revocation gate.
	clone.SetSkills(append(original, skills.Skill{Name: "another", Source: "plugin:consumer"}))
	materialized, err := NewLoadSkillTool(clone.env).Execute(context.Background(), `{"name":"dependent"}`)
	if err != nil {
		t.Fatal(err)
	}
	revoked.Store(true)
	for _, candidate := range []*Toolkit{kit, clone} {
		if _, ok := candidate.env.FindSkill("dependent"); ok {
			t.Fatal("revoked skill still resolves")
		}
		if got := candidate.env.SkillNames(); len(got) != 1 || got[0] != "independent" {
			t.Fatalf("revoked skill visible: %v", got)
		}
		if got := candidate.Skills(); len(got) != 1 || got[0].Name != "independent" {
			t.Fatalf("toolkit catalog leaked revoked skill: %v", got)
		}
		if got := candidate.FilterAvailableSkills(original); len(got) != 1 || got[0].Name != "independent" {
			t.Fatalf("rediscovered catalog leaked revoked skill: %v", got)
		}
		if _, err := NewLoadSkillTool(candidate.env).Execute(context.Background(), `{"name":"dependent"}`); err == nil {
			t.Fatal("load_skill read revoked instructions")
		}
	}
	if materialized == "" || original[0].Content != "old instructions" || len(original) != 2 {
		t.Fatal("revocation mutated a materialized snapshot")
	}
	// A recovered generation must not reactivate retained clones from the old one.
	kit.SetSkillsWithAvailability(original, func(skills.Skill) bool { return true })
	if _, ok := kit.env.FindSkill("dependent"); !ok {
		t.Fatal("new generation not available")
	}
	if _, ok := clone.env.FindSkill("dependent"); ok {
		t.Fatal("old generation revived")
	}
}

func TestSkillCatalogPublicationAndRevocationAreRaceSafe(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	gate := func(skill skills.Skill) bool { return skill.Source != "plugin:consumer" || !revoked.Load() }
	items := []skills.Skill{{Name: "dependent", Source: "plugin:consumer"}, {Name: "local", Source: "project"}}
	kit.SetSkillsWithAvailability(items, gate)
	clone, err := kit.CloneForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for j := 0; j < 200; j++ {
				kit.Skills()
				kit.env.FindSkill("dependent")
				kit.env.SkillNames()
				clone.env.VisibleSkills()
				kit.FilterAvailableSkills(items)
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 200; i++ {
			kit.SetSkills(items)
		}
	}()
	close(start)
	revoked.Store(true)
	workers.Wait()
	if len(kit.Skills()) != 1 || len(clone.Skills()) != 1 {
		t.Fatal("publishing skills removed revocation gate")
	}
	// Catalog ownership excludes mutation of callers' and readers' outer slices.
	items[0].Name = "changed"
	kit.SetSkillsWithAvailability([]skills.Skill{{Name: "stable"}}, nil)
	listed := kit.Skills()
	listed[0].Name = "mutated"
	if kit.Skills()[0].Name != "stable" {
		t.Fatal("caller mutated published catalog")
	}
}
